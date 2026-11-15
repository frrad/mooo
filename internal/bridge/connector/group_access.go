package connector

import (
	"context"
	"errors"
	"time"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
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
	kc.cacheMu.Lock()
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
	kc.cacheMu.Unlock()
	if classification == "" && kc.pausedDeliveries.Load() > 0 {
		classification = stateChatDeliveryPaused
	}
	if classification != "" {
		kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: classification})
	} else {
		kc.sendState(status.BridgeState{StateEvent: status.StateConnected})
	}
}

func (kc *KakaoClient) verifySourceLeave(ctx context.Context, chatID int64) error {
	p, err := kc.store.portalByKey(ctx, makePortalKey(chatID, kc.login.ID))
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
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	p, err := kc.store.portalByKey(ctx, makePortalKey(chatID, kc.login.ID))
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

// reconcileMissingGroups handles managed groups that are not ordinary groups
// in a completed full inventory. Absence alone is not removal evidence: even
// at a zero list token the server omits unchanged chats that LOGINLIST
// declared in chatIds/maxIds, so a quiet group is routinely missing. Only an
// explicit delChatIds entry revokes access here. A group already revoked by an
// earlier absence-based reading is re-verified against a fresh source roster,
// which restores forwarding only if the account is still a member; a failed
// re-verification keeps the group blocked and does not stop startup.
// excluded holds returned chats that were not ordinary groups, for diagnosis.
func (kc *KakaoClient) reconcileMissingGroups(ctx context.Context, c kakaoClient, seen map[int64]struct{}, excluded map[int64]chatmeta.ChatData, deleted map[int64]struct{}) error {
	portals, err := kc.store.portalsWithMXID(ctx)
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
		if chat, returned := excluded[chatID]; returned {
			kc.log().Warn().
				Int64("kakao_chat_id", chatID).
				Str("kakao_chat_type", chat.Type).
				Int64("kakao_link_id", chat.LinkID).
				Msg("Managed group returned in Kakao chat inventory, but not as an ordinary group")
		}
		if _, removed := deleted[chatID]; removed {
			if err = kc.recordSourceRemoval(ctx, chatID, "deleted_in_chat_inventory"); err != nil {
				return err
			}
			kc.sendState(status.BridgeState{StateEvent: status.StateUnknownError, Error: stateGroupAccessRemoved})
			if err = kc.applySourceLeave(ctx, chatID); err != nil {
				return errors.Join(errSourceAccessRemoved, errMembershipPending, err)
			}
			continue
		}
		if !meta.SourceRemoved {
			kc.log().Debug().Int64("kakao_chat_id", chatID).Msg("Managed group absent from Kakao chat inventory; absence is not removal evidence")
			continue
		}
		if err = kc.reverifySourceRemoval(ctx, c, chatID); err != nil {
			return err
		}
	}
	return nil
}

// reverifySourceRemoval reads a fresh source roster for a revoked group that
// the inventory did not list as deleted. Forwarding resumes only when the
// roster contains the account and Matrix membership converges with it.
func (kc *KakaoClient) reverifySourceRemoval(ctx context.Context, c kakaoClient, chatID int64) error {
	err := kc.refreshGroupMembership(ctx, c, chatID, false)
	if err == nil {
		kc.log().Info().Int64("kakao_chat_id", chatID).Msg("Re-verified Kakao source chat membership; forwarding restored")
		return nil
	}
	kc.log().Warn().Err(err).Int64("kakao_chat_id", chatID).Msg("Could not re-verify Kakao source chat membership; forwarding stays blocked")
	if errors.Is(err, errSourceAccessRemoved) {
		// convergeGroupMembership already recorded the removal.
		return nil
	}
	// The refresh paused the group; restore the stronger removal block.
	return kc.recordSourceRemoval(ctx, chatID, "rejoin_unverified")
}

// recordSourceRemoval blocks forwarding for chatID. reason names the evidence
// that triggered the block; the log line is the only place an operator can
// learn which chat the chat-agnostic bridge state refers to.
func (kc *KakaoClient) recordSourceRemoval(ctx context.Context, chatID int64, reason string) error {
	kc.cacheMu.Lock()
	if kc.sourceBlocked == nil {
		kc.sourceBlocked = map[int64]error{}
	}
	kc.sourceBlocked[chatID] = errSourceAccessRemoved
	kc.cacheMu.Unlock()
	portal, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(chatID, kc.login.ID))
	if err != nil {
		kc.log().Err(err).
			Int64("kakao_chat_id", chatID).
			Str("source_removal_reason", reason).
			Msg("Kakao account no longer in source chat; blocked in memory but failed to load portal")
		return err
	}
	kc.log().Warn().
		Int64("kakao_chat_id", chatID).
		Stringer("portal_mxid", portal.MXID).
		Str("source_removal_reason", reason).
		Msg("Kakao account no longer in source chat; blocking forwarding until rejoin is verified")
	return kc.savePortalMeta(ctx, portal, func(next *KakaoPortalMetadata) {
		next.SourceRemoved = true
	}, func(got *KakaoPortalMetadata) bool {
		return got.SourceRemoved
	})
}

func (kc *KakaoClient) checkSourceAccess(ctx context.Context, chatID int64) error {
	kc.cacheMu.Lock()
	blocked := kc.sourceBlocked[chatID]
	kc.cacheMu.Unlock()
	if blocked != nil {
		return blocked
	}
	saved, err := kc.store.portalByKey(ctx, makePortalKey(chatID, kc.login.ID))
	if err != nil || saved == nil {
		return err
	}
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
	return nil
}
