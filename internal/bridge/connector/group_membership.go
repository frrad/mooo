package connector

import (
	"context"
	"errors"
	"slices"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
)

var errMembershipPending = errors.New("connector: Matrix membership has not converged with the source roster; forwarding is paused")

const stateGroupMembershipPending status.BridgeStateErrorCode = "kakao-group-membership-pending"
const stateGroupMembershipInvalid status.BridgeStateErrorCode = "kakao-group-membership-invalid"

func init() {
	status.BridgeStateHumanErrors.Update(status.BridgeStateErrorMap{stateGroupMembershipInvalid: "Kakao delivered an undecodable membership notice. Forwarding stopped to prevent using stale access. Repair the decoder, then reconnect to verify the source roster.", stateGroupMembershipPending: "Matrix group membership has not converged with the source roster. Repair room membership or permissions, then reconnect to refresh before forwarding resumes."})
}

func (kc *KakaoClient) saveMembershipCheckpoint(ctx context.Context, p *bridgev2.Portal, pending bool, roster []int64) error {
	var sourceRemoved bool
	return kc.savePortalMeta(ctx, p, func(next *KakaoPortalMetadata) {
		next.GroupMembershipManaged = true
		next.MembershipPending = pending
		next.MembershipRoster = slices.Clone(roster)
		if !pending {
			next.SourceRemoved = false
		}
		sourceRemoved = next.SourceRemoved
	}, func(got *KakaoPortalMetadata) bool {
		return got.GroupMembershipManaged && got.MembershipPending == pending && got.SourceRemoved == sourceRemoved && slices.Equal(got.MembershipRoster, roster)
	})
}

// refreshGroupMembership uses a fresh full source roster rather than a possibly
// delayed notice delta. It clears access only after verifying Matrix convergence.
func (kc *KakaoClient) refreshGroupMembership(ctx context.Context, c kakaoClient, chatID int64, create bool) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(chatID, kc.login.ID))
	if err != nil {
		return err
	}
	kc.mu.Lock()
	if kc.sourceBlocked == nil {
		kc.sourceBlocked = map[int64]error{}
	}
	kc.sourceBlocked[chatID] = errMembershipPending
	kc.mu.Unlock()
	// Persist the pause before source or Matrix work, including a failed refresh.
	if err = kc.saveMembershipCheckpoint(ctx, p, true, nil); err != nil {
		return err
	}
	info, err := kc.chatInfoFromClient(ctx, p, c, true)
	if err != nil {
		return err
	}
	raw, err := c.MemberList(ctx, chatID, 0)
	if err != nil {
		return err
	}
	expected := map[networkid.UserID]bool{}
	for _, member := range raw.MemberIDs {
		user := makeUserID(member)
		if member <= 0 || expected[user] {
			return errInvalidMemberRoster
		}
		expected[user] = true
	}
	if !expected[makeUserID(kc.userID)] {
		if err = kc.recordSourceRemoval(ctx, chatID); err != nil {
			return err
		}
		return errSourceAccessRemoved
	}
	if info.Members == nil || !info.Members.IsFull || len(info.Members.MemberMap) != len(expected) {
		return errInvalidMemberRoster
	}
	for user := range info.Members.MemberMap {
		if !expected[user] {
			return errInvalidMemberRoster
		}
	}
	if err = kc.saveMembershipCheckpoint(ctx, p, true, raw.MemberIDs); err != nil {
		return err
	}
	key := p.PortalKey
	resync := &simplevent.ChatResync{EventMeta: simplevent.EventMeta{Type: bridgev2.RemoteEventChatResync, PortalKey: key, Sender: kc.selfSender(), CreatePortal: create}, GetChatInfoFunc: func(_ context.Context, target *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
		if target == nil || target.PortalKey != key {
			return nil, errChatInfoMismatch
		}
		return info, nil
	}}
	if !committable(kc.queue(resync)) {
		return errMembershipPending
	}
	// Portal creation may have supplied the room ID through the framework cache.
	p, err = kc.login.Bridge.GetPortalByKey(ctx, key)
	if err != nil {
		return err
	}
	if p.MXID == "" {
		return errMembershipPending
	}
	members, err := kc.login.Bridge.Matrix.GetMembers(ctx, p.MXID)
	if err != nil {
		return err
	}
	for user := range expected {
		ghost, ghostErr := kc.login.Bridge.GetGhostByID(ctx, user)
		if ghostErr != nil {
			return ghostErr
		}
		member := members[ghost.Intent.GetMXID()]
		if member == nil || member.Membership != event.MembershipJoin {
			return errMembershipPending
		}
	}
	for mxid, member := range members {
		user, isGhost := kc.login.Bridge.Matrix.ParseGhostMXID(mxid)
		if isGhost && !expected[user] && member.Membership.IsInviteOrJoin() {
			return errMembershipPending
		}
	}
	if err = kc.saveMembershipCheckpoint(ctx, p, false, raw.MemberIDs); err != nil {
		return err
	}
	kc.mu.Lock()
	delete(kc.sourceBlocked, chatID)
	kc.mu.Unlock()
	return nil
}

func (kc *KakaoClient) managedMembershipEvent(ctx context.Context, c kakaoClient, chatID int64, create bool) (bool, bool) {
	if kc.login == nil || kc.login.Bridge == nil || kc.login.Bridge.DB == nil {
		return false, false
	}
	p, err := kc.login.Bridge.DB.Portal.GetByKey(ctx, makePortalKey(chatID, kc.login.ID))
	if err != nil {
		return true, false
	}
	if p != nil && p.RoomType == database.RoomTypeDM {
		return false, false
	}
	err = kc.refreshGroupMembership(ctx, c, chatID, create)
	if err != nil {
		kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateGroupMembershipPending})
	} else if kc.IsLoggedIn() {
		kc.sendReadyState()
	}
	return true, err == nil
}
