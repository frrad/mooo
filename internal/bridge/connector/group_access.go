package connector

import (
	"context"
	"errors"
	"time"

	"github.com/frrad/mooo/internal/protocol/events"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

const stateGroupAccessRemoved status.BridgeStateErrorCode = "kakao-group-access-removed"

var errSourceAccessRemoved = errors.New("connector: the Kakao account left or was removed from this source chat; forwarding is blocked until source rejoining is verified")

func init() {
	status.BridgeStateHumanErrors.Update(status.BridgeStateErrorMap{stateGroupAccessRemoved: "The Kakao account left or was removed from a source chat. Forwarding for that chat is blocked until source rejoining is verified."})
}

// Transport readiness does not mean all source chats are authorized.
func (kc *KakaoClient) sendReadyState() {
	kc.mu.Lock()
	classification := status.BridgeStateErrorCode("")
	for _, blocked := range kc.sourceBlocked {
		if errors.Is(blocked, errSourceAccessRemoved) {
			classification = stateGroupAccessRemoved
			break
		}
		if blocked != nil {
			classification = stateGroupMembershipPending
		}
	}
	kc.mu.Unlock()
	if classification != "" {
		kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: classification})
	} else {
		kc.sendState(status.BridgeState{StateEvent: status.StateConnected})
	}
}

func (kc *KakaoClient) verifySourceLeave(ctx context.Context, chatID int64) error {
	if kc.login == nil || kc.login.Bridge == nil || kc.login.Bridge.DB == nil {
		return nil
	}
	p, err := kc.login.Bridge.DB.Portal.GetByKey(ctx, makePortalKey(chatID, kc.login.ID))
	if err != nil {
		return err
	}
	if p == nil || p.MXID == "" {
		return nil
	}
	ghost, err := kc.login.Bridge.GetGhostByID(ctx, makeUserID(kc.userID))
	if err != nil {
		return err
	}
	members, err := kc.login.Bridge.Matrix.GetMembers(ctx, p.MXID)
	if err != nil {
		return err
	}
	// bridgev2 applies a self leave to both the ghost and its associated Matrix
	// user (or double puppet). Verify both rather than trusting queue success.
	for _, mxid := range []id.UserID{ghost.Intent.GetMXID(), kc.login.UserMXID} {
		if member := members[mxid]; member != nil && member.Membership.IsInviteOrJoin() {
			return errMembershipPending
		}
	}
	return nil
}

// applySourceLeave bypasses QueueRemoteEvent: its MarkInPortal admission step
// invites the associated Matrix user, which is wrong for a source departure.
func (kc *KakaoClient) applySourceLeave(ctx context.Context, chatID int64) error {
	if kc.login == nil || kc.login.Bridge == nil || kc.login.Bridge.DB == nil {
		if !committable(kc.queue(kc.remoteEventFor(events.ChatLeft{ChatID: chatID}))) {
			return errMembershipPending
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	p, err := kc.login.Bridge.DB.Portal.GetByKey(ctx, makePortalKey(chatID, kc.login.ID))
	if err != nil {
		return err
	}
	if p == nil || p.MXID == "" {
		return nil
	}
	ghost, err := kc.login.Bridge.GetGhostByID(ctx, makeUserID(kc.userID))
	if err != nil {
		return err
	}
	members, err := kc.login.Bridge.Matrix.GetMembers(ctx, p.MXID)
	if err != nil {
		return err
	}
	var failures error
	// Remove the associated user first. Always attempt the ghost's self leave,
	// even when Matrix permissions prevent removing the associated user.
	for _, step := range []struct {
		intent bridgev2.MatrixAPI
		target id.UserID
	}{{kc.login.Bridge.Bot, kc.login.UserMXID}, {ghost.Intent, ghost.Intent.GetMXID()}} {
		intent, target := step.intent, step.target
		member := members[target]
		if member == nil || !member.Membership.IsInviteOrJoin() {
			continue
		}
		if target == kc.login.UserMXID {
			if dp := kc.login.User.DoublePuppet(ctx); dp != nil {
				intent = dp
			}
		}
		content := *member
		content.Membership = event.MembershipLeave
		_, sendErr := intent.SendState(ctx, p.MXID, event.StateMember, target.String(), &event.Content{Parsed: &content}, time.Time{})
		failures = errors.Join(failures, sendErr)
	}
	return errors.Join(failures, kc.verifySourceLeave(ctx, chatID))
}

// Only a completed full inventory can revoke access based on absence. Unknown
// legacy room types and unbound draft portals are not inferred to be groups.
func (kc *KakaoClient) reconcileMissingGroups(ctx context.Context, seen map[int64]struct{}) error {
	if kc.login == nil || kc.login.Bridge == nil || kc.login.Bridge.DB == nil {
		return nil
	}
	portals, err := kc.login.Bridge.DB.Portal.GetAll(ctx)
	if err != nil {
		return err
	}
	for _, p := range portals {
		if p.Receiver != kc.login.ID || p.MXID == "" {
			continue
		}
		meta, ok := p.Metadata.(*KakaoPortalMetadata)
		if !ok || meta == nil || !meta.GroupMembershipManaged && len(meta.MembershipRoster) == 0 {
			continue
		}
		chatID, parseErr := parseChatID(p.ID)
		if parseErr != nil {
			return parseErr
		}
		if _, present := seen[chatID]; present {
			continue
		}
		if err = kc.recordSourceRemoval(ctx, chatID); err != nil {
			return err
		}
		kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateGroupAccessRemoved})
		if err = kc.applySourceLeave(ctx, chatID); err != nil {
			return errors.Join(errSourceAccessRemoved, errMembershipPending, err)
		}
	}
	return nil
}

func (kc *KakaoClient) recordSourceRemoval(ctx context.Context, chatID int64) error {
	kc.mu.Lock()
	if kc.sourceBlocked == nil {
		kc.sourceBlocked = map[int64]error{}
	}
	kc.sourceBlocked[chatID] = errSourceAccessRemoved
	kc.mu.Unlock()
	if kc.login == nil || kc.login.Bridge == nil || kc.login.Bridge.DB == nil {
		return nil
	}
	portal, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(chatID, kc.login.ID))
	if err != nil {
		return err
	}
	meta, ok := portal.Metadata.(*KakaoPortalMetadata)
	if !ok || meta == nil {
		return errors.New("connector: source access metadata is unavailable")
	}
	next := *meta
	next.SourceRemoved = true
	portal.Metadata = &next
	if err = portal.Save(ctx); err != nil {
		portal.Metadata = meta
		return err
	}
	saved, err := kc.login.Bridge.DB.Portal.GetByKey(ctx, portal.PortalKey)
	if err != nil {
		return err
	}
	if saved == nil {
		return errors.New("connector: source access state disappeared")
	}
	got, ok := saved.Metadata.(*KakaoPortalMetadata)
	if !ok || got == nil || !got.SourceRemoved {
		return errors.New("connector: source access removal was not persisted")
	}
	return nil
}

func (kc *KakaoClient) checkSourceAccess(ctx context.Context, chatID int64, portal *bridgev2.Portal) error {
	kc.mu.Lock()
	blocked := kc.sourceBlocked[chatID]
	kc.mu.Unlock()
	if blocked != nil {
		return blocked
	}
	if kc.login != nil && kc.login.Bridge != nil && kc.login.Bridge.DB != nil {
		saved, err := kc.login.Bridge.DB.Portal.GetByKey(ctx, makePortalKey(chatID, kc.login.ID))
		if err != nil {
			return err
		}
		if saved != nil {
			meta, ok := saved.Metadata.(*KakaoPortalMetadata)
			if !ok || meta == nil {
				return errors.New("connector: source access metadata is unavailable")
			}
			if meta.SourceRemoved {
				return errSourceAccessRemoved
			}
			if meta.MembershipPending {
				return errMembershipPending
			}
		}
	} else if portal != nil {
		if meta, ok := portal.Metadata.(*KakaoPortalMetadata); ok && meta != nil && meta.SourceRemoved {
			return errSourceAccessRemoved
		}
		if meta, ok := portal.Metadata.(*KakaoPortalMetadata); ok && meta != nil && meta.MembershipPending {
			return errMembershipPending
		}
	}
	return nil
}
