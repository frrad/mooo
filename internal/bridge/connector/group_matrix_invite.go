package connector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chat"
)

var (
	errInviteNotRegularGroup = errors.New("connector: only regular KakaoTalk groups add members from Matrix")
	errInviteRefused         = errors.New("connector: KakaoTalk refused the invite")
	errInviteUnconfirmed     = errors.New("connector: KakaoTalk did not confirm the invite")
)

// teamChatMetaType marks a regular group as a team chat, whose members are
// managed over a separate HTTP path.
const teamChatMetaType = 15

// The Mac client translates these Loco statuses to its errors 53 and 54 and
// shows both as the blocked-friend alert.
var blockedFriendInviteStatuses = map[int32]bool{-402: true, -405: true}

// handleMatrixInvite adds a KakaoTalk user to the regular group with one
// ADDMEM, as any member can in the official client. The reply does not decide
// Matrix membership: unless KakaoTalk certainly refused, a fresh source
// roster joins the invited ghost or revokes its invite.
func (kc *KakaoClient) handleMatrixInvite(ctx context.Context, msg *bridgev2.MatrixMembershipChange, ghost *bridgev2.Ghost) error {
	refuse := func(err error, message string) error {
		revokeGhostMembership(ctx, msg, ghost)
		return bridgev2.WrapErrorInStatus(err).
			WithStatus(event.MessageStatusFail).
			WithErrorReason(event.MessageStatusUnsupported).
			WithIsCertain(true).
			WithSendNotice(true).
			WithMessage(message)
	}
	const notRegular = "Only regular KakaoTalk groups can add members from Matrix; invite them in KakaoTalk. The invite was revoked."
	if msg.Portal == nil || msg.Portal.RoomType == database.RoomTypeDM {
		return refuse(errInviteNotRegularGroup, notRegular)
	}
	chatID, err := parseChatID(msg.Portal.ID)
	if err != nil {
		return err
	}
	userID, err := parseUserID(string(ghost.ID))
	if err != nil {
		return err
	}
	if err = kc.checkSourceAccess(ctx, chatID, msg.Portal); err != nil {
		return err
	}
	kc.mu.Lock()
	c := kc.client
	kc.mu.Unlock()
	if c == nil {
		return bridgev2.ErrNotLoggedIn
	}
	info, err := c.ChatInfo(ctx, chatID)
	if err != nil {
		return err
	}
	data := info.ChatData
	if data.ChatID != chatID {
		return errChatInfoMismatch
	}
	if data.Type != "MultiChat" || data.LinkID != 0 {
		return refuse(errInviteNotRegularGroup, notRegular)
	}
	for _, meta := range data.ChatMetas {
		if meta.Type == teamChatMetaType {
			return refuse(errInviteNotRegularGroup, notRegular)
		}
	}
	roster, err := c.MemberList(ctx, chatID, 0)
	if err != nil {
		return err
	}
	for _, member := range roster.MemberIDs {
		if member == userID {
			kc.refreshMembership(c, chatID)
			return nil
		}
	}
	if err = kc.beginOutboundEvent(ctx, msg.Event); err != nil {
		return err
	}
	response, err := c.AddMembers(ctx, chat.AddMembersRequest{ChatID: chatID, MemberIDs: []int64{userID}})
	var statusErr client.StatusError
	switch {
	case errors.As(err, &statusErr) && blockedFriendInviteStatuses[statusErr.Status]:
		return refuse(errInviteRefused, "KakaoTalk refused the invite because this user is on your blocked friends list. The invite was revoked.")
	case errors.As(err, &statusErr):
		return refuse(errInviteRefused, fmt.Sprintf("KakaoTalk refused the invite (status %d). The invite was revoked.", statusErr.Status))
	}
	kc.refreshMembership(c, chatID)
	unconfirmed := func(message string) error {
		return bridgev2.WrapErrorInStatus(errInviteUnconfirmed).
			WithStatus(event.MessageStatusFail).
			WithIsCertain(false).
			WithSendNotice(true).
			WithMessage(message)
	}
	switch {
	case err != nil:
		return unconfirmed("KakaoTalk did not confirm the invite. It was not retried; the room follows KakaoTalk's member list.")
	case response.Warning != "":
		return unconfirmed(fmt.Sprintf("KakaoTalk returned a warning for the invite: %q. The room follows KakaoTalk's member list.", response.Warning))
	}
	return nil
}

// revokeGhostMembership clears a ghost's Matrix invite or ban by setting it
// to leave.
func revokeGhostMembership(ctx context.Context, msg *bridgev2.MatrixMembershipChange, ghost *bridgev2.Ghost) bool {
	if msg.Portal == nil || msg.Portal.MXID == "" || msg.Portal.Bridge == nil || msg.Portal.Bridge.Bot == nil || ghost.Intent == nil {
		return false
	}
	content := &event.Content{Parsed: &event.MemberEventContent{Membership: event.MembershipLeave, Reason: "KakaoTalk membership is managed in KakaoTalk"}}
	if _, err := msg.Portal.Bridge.Bot.SendState(ctx, msg.Portal.MXID, event.StateMember, string(ghost.Intent.GetMXID()), content, time.Time{}); err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to undo a rejected Matrix membership change")
		return false
	}
	return true
}
