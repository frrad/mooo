package connector

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/rs/zerolog"
	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
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
// ADDMEM, as any member can in the official client's invitation picker, which
// offers only non-members of the stored room. A confirmed reply's invitation
// log is applied to the stored roster like a NEWMEM feed, with MEMBER for the
// invitee, as the official client refreshes the selected members. Otherwise,
// unless KakaoTalk certainly refused, a fresh source roster joins the invited
// ghost or revokes its invite.
func (kc *KakaoClient) handleMatrixInvite(ctx context.Context, msg *bridgev2.MatrixMembershipChange, ghost *bridgev2.Ghost) error {
	refuse := func(err error, message string) error {
		revokeGhostMembership(ctx, msg, ghost)
		return certainRefusal(err, message)
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
	if err = kc.checkSourceAccess(ctx, chatID); err != nil {
		return err
	}
	kc.mu.Lock()
	c := kc.client
	kc.mu.Unlock()
	if c == nil {
		return bridgev2.ErrNotLoggedIn
	}
	roster, team, stored := kc.storedGroupRoster(ctx, chatID)
	if !stored {
		// Without a verified roster the group's kind and members are unknown.
		roster, team, err = kc.readInviteGroup(ctx, c, chatID)
		if errors.Is(err, errInviteNotRegularGroup) {
			return refuse(err, notRegular)
		} else if err != nil {
			return err
		}
	}
	if team {
		return refuse(errInviteNotRegularGroup, notRegular)
	}
	if slices.Contains(roster, userID) {
		if !stored {
			kc.refreshMembership(c, chatID, nil)
		}
		return nil
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
	var feed *membershipFeed
	if err == nil && response.Warning == "" && stored {
		feed = invitationFeed(chatID, response.ChatLog)
	}
	kc.refreshMembership(c, chatID, feed)
	unconfirmed := func(message string) error {
		// An unconfirmed invite keeps WrapErrorInStatus's generic reason.
		return noticeStatus(errInviteUnconfirmed, event.MessageStatusFail, event.MessageStatusGenericError, false, message)
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

// storedGroupRoster returns a managed group's verified roster and whether its
// stored chat data marks it a team chat. It reports false when either is not
// stored, so the caller must read the source.
func (kc *KakaoClient) storedGroupRoster(ctx context.Context, chatID int64) (roster []int64, team, ok bool) {
	roster = kc.verifiedRoster(ctx, chatID)
	if len(roster) == 0 {
		return nil, false, false
	}
	q := kc.metadataRefresh
	q.mu.Lock()
	task, found := q.snapshots[chatID]
	q.mu.Unlock()
	data := task.snapshot.data
	if !found || data.Type != "MultiChat" || data.LinkID != 0 {
		return nil, false, false
	}
	return roster, slices.ContainsFunc(data.ChatMetas, func(meta chatmeta.ChatMeta) bool { return meta.Type == teamChatMetaType }), true
}

// readInviteGroup reads an unverified group's kind and roster from the source.
func (kc *KakaoClient) readInviteGroup(ctx context.Context, c kakaoClient, chatID int64) (roster []int64, team bool, err error) {
	info, err := c.ChatInfo(ctx, chatID)
	if err != nil {
		return nil, false, err
	}
	data := info.ChatData
	if data.ChatID != chatID {
		return nil, false, errChatInfoMismatch
	}
	if data.Type != "MultiChat" || data.LinkID != 0 {
		return nil, false, errInviteNotRegularGroup
	}
	team = slices.ContainsFunc(data.ChatMetas, func(meta chatmeta.ChatMeta) bool { return meta.Type == teamChatMetaType })
	if team {
		return nil, true, nil
	}
	members, err := c.MemberList(ctx, chatID, 0)
	if err != nil {
		return nil, false, err
	}
	return members.MemberIDs, false, nil
}

// invitationFeed decodes the invitation log of a confirmed ADDMEM reply, or
// returns nil when the reply carries no usable membership feed for chatID.
func invitationFeed(chatID int64, chatLog bson.Raw) *membershipFeed {
	if len(chatLog) == 0 {
		return nil
	}
	evt, err := events.DecodeChatLog(chatID, chatLog)
	added, ok := evt.(events.MemberAdded)
	if err != nil || !ok || added.ChatID != chatID {
		return nil
	}
	feed := &membershipFeed{}
	for _, member := range added.Members {
		feed.invitees = append(feed.invitees, member.UserID)
	}
	return feed
}
