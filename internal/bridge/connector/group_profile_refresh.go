package connector

import (
	"context"
	"errors"
	"io"
	"net"
	"sort"
	"time"

	"github.com/frrad/mooo/internal/client"

	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/status"
)

// refreshOneGroupProfiles is bridge polling policy, not a native push mapping.
// The event pump calls it at most once per minute, rotating through existing
// managed groups. Fresh membership authorizes profile application; it never
// uses cached identities to rejoin a departed member or creates a new portal.
//
// It reports whether the refresh failed on the connection. The caller then
// ends the session: the refreshed group stays paused, and recovery's group
// discovery refreshes it on the next session. Reporting a membership problem
// for a network failure would ask the operator to repair Matrix rooms that are
// fine, and the paused group would otherwise wait for an unrelated reconnect.
func (kc *KakaoClient) refreshOneGroupProfiles(c kakaoClient) (connectionFailed bool) {
	if !kc.groupGate.TryLock() {
		return false
	}
	defer kc.groupGate.Unlock()
	kc.mu.Lock()
	stopping := kc.stopping || kc.client != c
	kc.mu.Unlock()
	if stopping {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := kc.store.userPortals(ctx, kc.login.UserLogin)
	if err != nil {
		kc.log().Warn().Msg("Could not list groups for profile refresh")
		return false
	}
	var eligible []*database.Portal
	for _, row := range rows {
		p, err := kc.store.portalByKey(ctx, row.Portal)
		if err != nil {
			kc.log().Warn().Msg("Could not read group for profile refresh")
			continue
		}
		if p == nil || p.MXID == "" || p.Receiver != kc.login.ID || p.RoomType == database.RoomTypeDM {
			continue
		}
		meta, ok := p.Metadata.(*KakaoPortalMetadata)
		if !ok || meta == nil || meta.SourceRemoved || meta.MembershipPending {
			continue
		}
		knownRegularGroup := meta.GroupMembershipManaged
		if loginMeta, ok := kc.login.Metadata.(*UserLoginMetadata); ok && loginMeta != nil {
			if attempt, exists := loginMeta.GroupCreates[string(p.MXID)]; exists && attempt.Bound && makePortalKey(attempt.ChatID, kc.login.ID) == p.PortalKey {
				knownRegularGroup = true
			}
		}
		if !knownRegularGroup {
			continue
		}
		eligible = append(eligible, p)
	}
	if len(eligible) == 0 {
		return false
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].ID < eligible[j].ID })
	next := eligible[0]
	for _, p := range eligible {
		if string(p.ID) > kc.profileRefreshAfter {
			next = p
			break
		}
	}
	kc.profileRefreshAfter = string(next.ID)
	chatID, err := parseChatID(next.ID)
	if err != nil {
		return false
	}
	if err = kc.refreshGroupMembership(ctx, c, chatID, false); err != nil {
		// The membership path retains its durable access pause on failure.
		// Never log a server response containing profile fields or retry here.
		if connectionFailure(err) {
			kc.log().Warn().Msg("Group profile refresh failed on the KakaoTalk connection; ending the session so recovery refreshes the group")
			return true
		}
		kc.log().Warn().Msg("Group profile refresh did not converge; reconnect to repair pending membership")
		classification := stateGroupMembershipPending
		if errors.Is(err, errSourceAccessRemoved) {
			classification = stateGroupAccessRemoved
		}
		kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: classification})
	}
	return false
}

// connectionFailure reports whether a source request failed on the
// connection rather than being answered: the session closed, the transport
// failed, or no reply arrived in time.
func connectionFailure(err error) bool {
	if errors.Is(err, client.ErrClosed) || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
}
