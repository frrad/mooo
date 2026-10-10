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
