package connector

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

var _ bridgev2.PowerLevelHandlingNetworkAPI = (*KakaoClient)(nil)

var errNoSourceRoles = errors.New("connector: KakaoTalk regular groups have no member roles")

// HandleMatrixPowerLevels keeps KakaoTalk members at the default level.
// Regular groups have no roles, so a change to a KakaoTalk member's level has
// no source meaning: it is rejected and the previous levels are restored.
// Levels of Matrix-only users are left to Matrix.
func (kc *KakaoClient) HandleMatrixPowerLevels(ctx context.Context, msg *bridgev2.MatrixPowerLevelChange) (bool, error) {
	if msg == nil {
		return false, nil
	}
	touchesMember := false
	for _, change := range msg.Users {
		if change == nil {
			continue
		}
		if _, isGhost := change.Target.(*bridgev2.Ghost); isGhost {
			touchesMember = true
		}
	}
	if !touchesMember {
		return false, nil
	}
	status := bridgev2.WrapErrorInStatus(errNoSourceRoles).
		WithStatus(event.MessageStatusFail).
		WithErrorReason(event.MessageStatusUnsupported).
		WithIsCertain(true).
		WithSendNotice(true).
		WithMessage("KakaoTalk regular groups have no member roles, so KakaoTalk members keep the default power level. The previous levels were restored.")
	if msg.PrevContent == nil || msg.Portal == nil || msg.Portal.MXID == "" || msg.Portal.Bridge == nil || msg.Portal.Bridge.Bot == nil {
		return false, status.WithMessage("KakaoTalk regular groups have no member roles; the power level change does not apply to KakaoTalk members.")
	}
	content := &event.Content{Parsed: msg.PrevContent}
	if _, err := msg.Portal.Bridge.Bot.SendState(ctx, msg.Portal.MXID, event.StatePowerLevels, "", content, time.Time{}); err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to restore power levels after a rejected change")
		return false, status.WithMessage("KakaoTalk regular groups have no member roles; the previous power levels could not be restored.")
	}
	return false, status
}

var _ bridgev2.MembershipHandlingNetworkAPI = (*KakaoClient)(nil)

var errMembershipNotBridged = errors.New("connector: KakaoTalk membership cannot be changed from Matrix")

// HandleMatrixMembership keeps KakaoTalk members' Matrix membership equal to
// the source. An invite of a KakaoTalk user adds them to a regular group;
// regular groups have no kick, so a kick or ban of a KakaoTalk member is
// rejected and undone in Matrix. Self and Matrix-only changes have no source
// meaning and pass silently.
func (kc *KakaoClient) HandleMatrixMembership(ctx context.Context, msg *bridgev2.MatrixMembershipChange) (*bridgev2.MatrixMembershipResult, error) {
	if msg == nil {
		return nil, nil
	}
	ghost, ok := msg.Target.(*bridgev2.Ghost)
	if !ok || ghost == nil || msg.Type.IsSelf {
		return nil, nil
	}
	var message string
	switch msg.Type {
	case bridgev2.Kick, bridgev2.BanJoined:
		message = "KakaoTalk regular groups have no way to remove a member; the member stays in KakaoTalk and was restored in Matrix."
	case bridgev2.BanLeft, bridgev2.BanInvited:
		message = "Banning KakaoTalk users is not supported; the ban was undone."
	case bridgev2.Invite:
		return nil, kc.handleMatrixInvite(ctx, msg, ghost)
	default:
		return nil, nil
	}
	status := bridgev2.WrapErrorInStatus(errMembershipNotBridged).
		WithStatus(event.MessageStatusFail).
		WithErrorReason(event.MessageStatusUnsupported).
		WithIsCertain(true).
		WithSendNotice(true).
		WithMessage(message)
	if msg.Portal == nil || msg.Portal.MXID == "" || msg.Portal.Bridge == nil || msg.Portal.Bridge.Bot == nil || ghost.Intent == nil {
		return nil, status
	}
	bot, room, ghostMXID := msg.Portal.Bridge.Bot, msg.Portal.MXID, ghost.Intent.GetMXID()
	log := zerolog.Ctx(ctx)
	// A ban is cleared by setting the ghost to leave.
	if msg.Type.To == event.MembershipBan {
		if !revokeGhostMembership(ctx, msg, ghost) {
			return nil, status
		}
	}
	// A member removed in Matrix is still in the KakaoTalk group.
	if msg.Type.From == event.MembershipJoin {
		if err := bot.EnsureInvited(ctx, room, ghostMXID); err != nil {
			log.Warn().Err(err).Msg("Failed to re-invite a KakaoTalk member after a rejected removal")
			return nil, status
		}
		if err := ghost.Intent.EnsureJoined(ctx, room); err != nil {
			log.Warn().Err(err).Msg("Failed to restore a KakaoTalk member after a rejected removal")
		}
	}
	return nil, status
}

var _ bridgev2.MuteHandlingNetworkAPI = (*KakaoClient)(nil)

// HandleMute accepts a Matrix mute as the Matrix user's own setting. The Mac
// client keeps per-chat notifications local and sends no request, so nothing
// is sent to KakaoTalk and no shared state changes.
func (kc *KakaoClient) HandleMute(context.Context, *bridgev2.MatrixMute) error {
	return nil
}

// refreshMembershipAsync must not run in the portal event loop: the roster
// refresh queues a resync into that same portal and waits for it.
func (kc *KakaoClient) refreshMembershipAsync(c kakaoClient, chatID int64) {
	go func() {
		kc.groupGate.Lock()
		defer kc.groupGate.Unlock()
		kc.managedMembershipEvent(context.Background(), c, chatID, false)
	}()
}
