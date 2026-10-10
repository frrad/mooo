package connector

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
)

var photoHTTPClient = http.DefaultClient

// transientTransferError marks a media transfer failure that replay may
// recover. It wraps bridgev2.ErrIgnoringRemoteEvent so the framework does not
// post its generic error notice on each attempt; the event has no mapping, so
// handleEvent and history backfill still retain source progress.
func transientTransferError(message string) error {
	return fmt.Errorf("%w: %s", bridgev2.ErrIgnoringRemoteEvent, message)
}

var errPhotoTransfer = transientTransferError("connector: Kakao photo transfer failed")

func placeholderUserName(userID int64) string {
	return fmt.Sprintf("KakaoTalk user %d", userID)
}

// kakaoTime converts a Kakao sendAt value, in Unix seconds. A zero value
// yields the zero time, which the bridge replaces with the receive time.
func kakaoTime(seconds int64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0)
}

// remoteEventFor maps a Kakao event to a bridge remote event. Every message
// event must produce one, because the client commits message positions
// strictly in delivery order per chat: a skipped message would block all
// later commits in its chat. Kinds that cannot be rendered yet become notices.
func (kc *KakaoClient) remoteEventFor(evt events.Event) bridgev2.RemoteEvent {
	switch evt := evt.(type) {
	case events.MiniTextMessage:
		return kc.miniTextEvent(evt)
	case events.PostMessage:
		return newMessage(kc.messageMeta(evt.ChatID, evt.LogID, evt.AuthorID, evt.SentAt), makeMessageID(evt.ChatID, evt.LogID), evt, convertPost)
	case events.VoteMessage:
		return newMessage(kc.messageMeta(evt.ChatID, evt.LogID, evt.AuthorID, evt.SentAt), makeMessageID(evt.ChatID, evt.LogID), evt, convertVote)
	case events.LocationMessage:
		return newMessage(kc.messageMeta(evt.ChatID, evt.LogID, evt.AuthorID, evt.SentAt), makeMessageID(evt.ChatID, evt.LogID), evt, convertLocation)
	case events.ProfileMessage:
		return newMessage(kc.messageMeta(evt.ChatID, evt.LogID, evt.AuthorID, evt.SentAt), makeMessageID(evt.ChatID, evt.LogID), evt, convertProfile)
	case events.TextMessage:
		return newMessage(kc.messageMeta(evt.ChatID, evt.LogID, evt.AuthorID, evt.SentAt), makeMessageID(evt.ChatID, evt.LogID), evt, convertText)
	case events.ReplyMessage:
		return newMessage(kc.messageMeta(evt.ChatID, evt.LogID, evt.AuthorID, evt.SentAt), makeMessageID(evt.ChatID, evt.LogID), evt, convertReply)
	case events.StickerMessage:
		return newMessage(kc.messageMeta(evt.ChatID, evt.LogID, evt.AuthorID, evt.SentAt), makeMessageID(evt.ChatID, evt.LogID), evt, convertSticker)
	case events.ContactMessage:
		c := evt.Message
		return newMessage(kc.messageMeta(c.ChatID, c.LogID, c.AuthorID, c.SentAt), makeMessageID(c.ChatID, c.LogID), evt, convertContact)
	case events.FileMessage:
		f := evt.Message
		return newMessage(kc.messageMeta(f.ChatID, f.LogID, f.AuthorID, f.SentAt), makeMessageID(f.ChatID, f.LogID), evt, convertFile)
	case events.AudioMessage:
		a := evt.Message
		return newMessage(kc.messageMeta(a.ChatID, a.LogID, a.AuthorID, a.SentAt), makeMessageID(a.ChatID, a.LogID), evt, convertAudio)
	case events.VideoMessage:
		a := evt.Message
		return newMessage(kc.messageMeta(a.ChatID, a.LogID, a.AuthorID, a.SentAt), makeMessageID(a.ChatID, a.LogID), evt, convertVideo)
	case events.MultiPhotoMessage:
		return kc.albumEvent(evt)
	case events.PhotoMessage:
		chatID, logID := evt.Message.ChatID, evt.Message.LogID
		return newMessage(kc.messageMeta(chatID, logID, evt.Message.AuthorID, evt.Message.SentAt), makeMessageID(chatID, logID), evt, convertPhoto)
	case events.MessageEdited:
		return kc.editEvent(evt)
	case events.MessageDeleted:
		return kc.deletionEvent(evt)
	case events.DeletedMessage:
		return kc.deletedMessageEvent(evt)
	case events.MessageGap:
		metadata := newKakaoMessageMetadata(evt.ChatID, evt.LogID, evt.AuthorID, evt.Type, "[message unavailable]", 0)
		metadata.ConversionGap = "malformed_payload"
		return newMessage(kc.messageMeta(evt.ChatID, evt.LogID, evt.AuthorID, evt.SentAt), makeMessageID(evt.ChatID, evt.LogID), noticeData{
			Body: "A KakaoTalk message could not be displayed (malformed_payload).", Metadata: metadata,
		}, convertNoticeWithMetadata)
	case events.UnsupportedMessage:
		notice := fmt.Sprintf("A KakaoTalk message of unsupported type %d was sent.", evt.Type)
		return newMessage(kc.messageMeta(evt.ChatID, evt.LogID, 0, 0), makeMessageID(evt.ChatID, evt.LogID), noticeData{
			Body: notice, Metadata: newKakaoMessageMetadata(evt.ChatID, evt.LogID, 0, evt.Type, "", 0),
		}, convertNoticeWithMetadata)
	case events.MemberAdded:
		return kc.memberChange(evt.ChatID, evt.LogID, 0, evt.Members, true)
	case events.MemberRemoved:
		return kc.memberChange(evt.ChatID, evt.LogID, 0, []events.MemberIdentity{{UserID: evt.UserID, UserType: evt.UserType}}, false)
	case events.ChatStatusChanged:
		return kc.chatResync(evt.ChatID, 0, evt.PlusUserID)
	case events.ChatMoimMetaChanged:
		return kc.announcementResync(evt.ChatID)
	case events.ChatMCMetaChanged:
		if evt.Type == "name" || evt.Type == "imagePath" {
			// Refresh the connected profile instead of applying potentially
			// delayed personal notice values to current Matrix state.
			resync := kc.chatResync(evt.ChatID, 0, kc.userID).(*simplevent.ChatResync)
			resync.GetChatInfoFunc = func(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
				if ctx == nil {
					return nil, bridgev2.ErrNotLoggedIn
				}
				if portal == nil || portal.PortalKey != makePortalKey(evt.ChatID, kc.login.ID) {
					return nil, errChatInfoMismatch
				}
				ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				c, err := kc.metadataClient()
				if err != nil {
					return nil, err
				}
				return kc.chatInfoFromClient(ctx, portal, c, false, true)
			}
			return resync
		}
		return nil
	case events.ChatMetaChanged:
		return kc.chatResync(evt.ChatID, 0, evt.AuthorID)
	case events.ChatLeft:
		members := bridgev2.ChatMemberMap{}.Set(bridgev2.ChatMember{
			EventSender: kc.selfSender(),
			Membership:  event.MembershipLeave,
		})
		return &simplevent.ChatInfoChange{
			EventMeta: simplevent.EventMeta{
				Type:      bridgev2.RemoteEventChatInfoChange,
				PortalKey: makePortalKey(evt.ChatID, kc.login.ID),
				// Use the bot to avoid rejoining the departed ghost when
				// the framework also removes its associated Matrix user.
				Sender: bridgev2.EventSender{},
			},
			ChatInfoChange: &bridgev2.ChatInfoChange{MemberChanges: &bridgev2.ChatMemberList{MemberMap: members}},
		}
	default:
		return nil
	}
}

// chatInfoChange asks the bridge to refresh source-backed room metadata. The
// Kakao event decoders intentionally leave status/meta payloads opaque, so
// these events only trigger the bounded CHATINFO/MEMLIST/MEMBER read.
func (kc *KakaoClient) chatResync(chatID, logID, authorID int64) bridgev2.RemoteEvent {
	return &simplevent.ChatResync{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventChatResync,
			PortalKey: makePortalKey(chatID, kc.login.ID),
			Sender:    kc.senderFor(authorID),
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Int64("kakao_chat_id", chatID).Int64("kakao_log_id", logID)
			},
		},
		GetChatInfoFunc: func(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
			if ctx == nil {
				return nil, bridgev2.ErrNotLoggedIn
			}
			if portal == nil {
				return nil, errChatInfoMismatch
			}
			boundChatID, err := parseChatID(portal.ID)
			if err != nil || boundChatID != chatID {
				return nil, errChatInfoMismatch
			}
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			return kc.GetChatInfo(ctx, portal)
		},
	}
}

func (kc *KakaoClient) memberChange(chatID, logID, authorID int64, identities []events.MemberIdentity, join bool) bridgev2.RemoteEvent {
	portalKey := makePortalKey(chatID, kc.login.ID)
	return &chatInfoChangeEvent{
		EventMeta: simplevent.EventMeta{
			Type:         bridgev2.RemoteEventChatInfoChange,
			CreatePortal: false,
			PortalKey:    portalKey,
			Sender:       kc.senderFor(authorID),
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Int64("kakao_chat_id", chatID).Int64("kakao_log_id", logID)
			},
		},
		getCreationInfo: func(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
			if portal == nil || portal.PortalKey != portalKey {
				return nil, errChatInfoMismatch
			}
			return kc.getChatInfo(ctx, portal, true)
		},
		getInfo: func(ctx context.Context) (*bridgev2.ChatInfo, error) {
			portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: portalKey}}
			return kc.GetChatInfo(ctx, portal)
		},
		getChanges: func(ctx context.Context) (*bridgev2.ChatMemberList, error) {
			return kc.memberChanges(ctx, chatID, identities, join)
		},
	}
}

type chatInfoChangeEvent struct {
	simplevent.EventMeta
	getCreationInfo func(context.Context, *bridgev2.Portal) (*bridgev2.ChatInfo, error)
	getInfo         func(context.Context) (*bridgev2.ChatInfo, error)
	getChanges      func(context.Context) (*bridgev2.ChatMemberList, error)
}

var _ bridgev2.RemoteChatInfoChange = (*chatInfoChangeEvent)(nil)
var _ bridgev2.RemoteChatResyncWithInfo = (*chatInfoChangeEvent)(nil)

func (evt *chatInfoChangeEvent) GetChatInfoChange(ctx context.Context) (*bridgev2.ChatInfoChange, error) {
	if ctx == nil {
		return nil, bridgev2.ErrNotLoggedIn
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	info, err := evt.getInfo(ctx)
	if err != nil {
		return nil, err
	}
	changes, err := evt.getChanges(ctx)
	if err != nil {
		return nil, err
	}
	return &bridgev2.ChatInfoChange{ChatInfo: info, MemberChanges: changes}, nil
}

func (kc *KakaoClient) memberChanges(ctx context.Context, chatID int64, identities []events.MemberIdentity, join bool) (*bridgev2.ChatMemberList, error) {
	ids := make([]int64, 0, len(identities))
	seen := make(map[int64]struct{}, len(identities))
	for _, identity := range identities {
		if identity.UserID <= 0 || join && identity.UserID == kc.userID {
			continue
		}
		if _, ok := seen[identity.UserID]; ok {
			continue
		}
		seen[identity.UserID] = struct{}{}
		ids = append(ids, identity.UserID)
	}
	if !join {
		members := bridgev2.ChatMemberMap{}
		for _, userID := range ids {
			sender := bridgev2.EventSender{Sender: makeUserID(userID)}
			if userID == kc.userID {
				sender = kc.selfSender()
			}
			members.Set(bridgev2.ChatMember{EventSender: sender, Membership: event.MembershipLeave})
		}
		return &bridgev2.ChatMemberList{MemberMap: members}, nil
	}
	if len(ids) == 0 {
		return nil, nil
	}
	members := bridgev2.ChatMemberMap{}
	for _, userID := range ids {
		members.Set(bridgev2.ChatMember{EventSender: bridgev2.EventSender{Sender: makeUserID(userID)}, Membership: event.MembershipJoin})
	}
	c, err := kc.metadataClient()
	if err != nil {
		return &bridgev2.ChatMemberList{MemberMap: members}, nil
	}
	profiles, err := c.Members(ctx, chatID, ids)
	if err != nil {
		return &bridgev2.ChatMemberList{MemberMap: members}, nil
	}
	requested := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		requested[id] = struct{}{}
	}
	for _, profile := range profiles {
		if _, ok := requested[profile.UserID]; !ok || profile.UserID <= 0 || profile.UserID == kc.userID {
			continue
		}
		kc.mu.Lock()
		kc.profiles[profile.UserID] = profile
		kc.mu.Unlock()
		member := members[makeUserID(profile.UserID)]
		member.UserInfo = userInfoForMember(profile)
		members[makeUserID(profile.UserID)] = member
	}
	return &bridgev2.ChatMemberList{MemberMap: members}, nil
}

// gapNotice tells the room that messages up to targetMax could not be
// recovered. Its ID is derived from the gap so repeated reconnects that hit
// the same gap post it once.
func (kc *KakaoClient) gapNotice(chatID, targetMax int64) bridgev2.RemoteEvent {
	id := networkid.MessageID(fmt.Sprintf("gap:%d:%d", chatID, targetMax))
	return newMessage(kc.messageMeta(chatID, 0, 0, 0), id,
		"Some KakaoTalk messages sent while the bridge was disconnected could not be recovered.", convertNotice)
}

func newMessage[T any](
	meta simplevent.EventMeta,
	id networkid.MessageID,
	data T,
	convert func(context.Context, *bridgev2.Portal, bridgev2.MatrixAPI, T) (*bridgev2.ConvertedMessage, error),
) *simplevent.Message[T] {
	originalMutation := meta.MutateContextFunc
	meta.MutateContextFunc = func(ctx context.Context) context.Context {
		if originalMutation != nil {
			ctx = originalMutation(ctx)
		}
		return context.WithValue(ctx, historyDeliveryKey, &historySendState{})
	}
	return &simplevent.Message[T]{
		EventMeta: meta,
		ID:        id,
		Data:      data,
		ConvertMessageFunc: func(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, value T) (*bridgev2.ConvertedMessage, error) {
			ctx, release := withConnectionLifecycle(ctx)
			defer release()
			converted, err := convert(ctx, portal, intent, value)
			if err != nil || converted == nil {
				return converted, err
			}
			if _, ok := ctx.Value(historyDeliveryKey).(*historySendState); ok {
				if err := markMatrixTransactions(portal, intent, id, converted, false); err != nil {
					return nil, err
				}
			}
			return converted, nil
		},
	}
}

// connectionLifecycleKey carries the connection lifecycle context from event
// creation into conversion. The SDK converts on the portal's own background
// context, which Disconnect cannot cancel.
type connectionLifecycleKey struct{}

// withConnectionLifecycle bounds a conversion by the connection that admitted
// its event, so Disconnect interrupts media transfers instead of waiting for
// them. The returned release must be called when conversion returns.
func withConnectionLifecycle(ctx context.Context) (context.Context, func()) {
	lifecycle, ok := ctx.Value(connectionLifecycleKey{}).(context.Context)
	if !ok || lifecycle == nil {
		return ctx, func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(lifecycle, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func (kc *KakaoClient) messageMeta(chatID, logID, authorID, sentAt int64) simplevent.EventMeta {
	lifecycle := kc.connectionLifecycle()
	return simplevent.EventMeta{
		MutateContextFunc: func(ctx context.Context) context.Context {
			if lifecycle == nil {
				return ctx
			}
			return context.WithValue(ctx, connectionLifecycleKey{}, lifecycle)
		},
		Type:      bridgev2.RemoteEventMessage,
		PortalKey: makePortalKey(chatID, kc.login.ID),
		LogContext: func(c zerolog.Context) zerolog.Context {
			return c.Int64("kakao_chat_id", chatID).Int64("kakao_log_id", logID)
		},
		Sender:       kc.senderFor(authorID),
		CreatePortal: true,
		Timestamp:    kakaoTime(sentAt),
	}
}

// senderFor maps a Kakao author. Messages this account wrote on another
// device are marked as from this login so double puppeting applies. An
// unknown author (zero) leaves the sender empty, which the bridge renders as
// its bot.
func (kc *KakaoClient) senderFor(authorID int64) bridgev2.EventSender {
	switch {
	case authorID <= 0:
		return bridgev2.EventSender{}
	case authorID == kc.userID:
		return kc.selfSender()
	default:
		return bridgev2.EventSender{Sender: makeUserID(authorID)}
	}
}

func convertText(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg events.TextMessage) (*bridgev2.ConvertedMessage, error) {
	metadata := newKakaoMessageMetadata(msg.ChatID, msg.LogID, msg.AuthorID, chat.TextType, msg.Message, 0)
	metadata.Revision = msg.Revision
	return messageWithMetadata(event.MsgText, msg.Message, metadata), nil
}

func convertReply(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg events.ReplyMessage) (*bridgev2.ConvertedMessage, error) {
	converted, err := replyContent(ctx, portal, intent, msg)
	if err != nil {
		return nil, err
	}
	if msg.Source.LogID <= 0 {
		return converted, nil
	}
	target := networkid.MessageOptionalPartID{MessageID: makeMessageID(msg.ChatID, msg.Source.LogID)}
	if replySourceMissing(ctx, portal, target) && msg.Source.Message != "" {
		// The framework drops a relation to an unbridged message. Keep the
		// context Kakao embedded in the reply as a quote instead.
		content := converted.Parts[0].Content
		body := content.Body
		content.Body = quotedReplyBody(msg.Source.Message) + "\n\n" + body
		if converted.Parts[0].Type != event.EventSticker {
			content.Format = event.FormatHTML
			content.FormattedBody = "<blockquote>" + htmlLines(msg.Source.Message) + "</blockquote>" + htmlLines(body)
		}
		return converted, nil
	}
	converted.ReplyTo = &target
	return converted, nil
}

// replyContent converts what a reply carries: its text, a supported sticker,
// or an explicit notice for an attachment the bridge cannot render.
func replyContent(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg events.ReplyMessage) (*bridgev2.ConvertedMessage, error) {
	metadata := newKakaoMessageMetadata(msg.ChatID, msg.LogID, msg.AuthorID, chat.ReplyType, msg.Message, 0)
	switch {
	case msg.Attachment.Type == 0:
		return messageWithMetadata(event.MsgText, msg.Message, metadata), nil
	case msg.Attachment.Sticker != nil && msg.Attachment.Only:
		part, err := stickerPart(ctx, portal, intent, events.StickerMessage{ChatID: msg.ChatID, LogID: msg.LogID, AuthorID: msg.AuthorID, SentAt: msg.SentAt, Type: msg.Attachment.Type, Attachment: *msg.Attachment.Sticker})
		if err != nil {
			return nil, err
		}
		// Keep the reply's own type so a later Matrix reply quotes it correctly.
		if partMetadata, ok := part.DBMetadata.(*KakaoMessageMetadata); ok {
			partMetadata.Type = chat.ReplyType
		}
		return &bridgev2.ConvertedMessage{Parts: []*bridgev2.ConvertedMessagePart{part}}, nil
	case msg.Attachment.Sticker != nil:
		// Not observed: a sticker with its own text. Keep the text visible and
		// mark the sticker explicitly rather than dropping either.
		return messageWithMetadata(event.MsgText, msg.Message+"\n\n(KakaoTalk sticker)", metadata), nil
	default:
		notice := fmt.Sprintf("A KakaoTalk reply contained an attachment that cannot be displayed (type %d).", msg.Attachment.Type)
		if msg.Attachment.Only || msg.Message == "" {
			metadata.ConversionGap = "unsupported_reply_attachment"
			return messageWithMetadata(event.MsgNotice, notice, metadata), nil
		}
		return messageWithMetadata(event.MsgText, msg.Message+"\n\n("+notice+")", metadata), nil
	}
}

// replySourceMissing reports a reply source confirmed absent from this
// portal's messages. Lookup failures leave the relation to the framework.
func replySourceMissing(ctx context.Context, portal *bridgev2.Portal, target networkid.MessageOptionalPartID) bool {
	if portal == nil || portal.Portal == nil || portal.Bridge == nil || portal.Bridge.DB == nil {
		return false
	}
	row, err := portal.Bridge.DB.Message.GetFirstOrSpecificPartByID(ctx, portal.Receiver, target)
	return err == nil && row == nil
}

func quotedReplyBody(source string) string {
	return "> " + strings.ReplaceAll(source, "\n", "\n> ")
}

func htmlLines(text string) string {
	return strings.ReplaceAll(html.EscapeString(text), "\n", "<br>")
}

func convertNotice(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, notice string) (*bridgev2.ConvertedMessage, error) {
	return textMessage(event.MsgNotice, notice), nil
}

type noticeData struct {
	Body     string
	Metadata *KakaoMessageMetadata
}

func convertNoticeWithMetadata(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, notice noticeData) (*bridgev2.ConvertedMessage, error) {
	return messageWithMetadata(event.MsgNotice, notice.Body, notice.Metadata), nil
}

func textMessage(msgType event.MessageType, body string) *bridgev2.ConvertedMessage {
	return &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType: msgType,
				Body:    body,
			},
		}},
	}
}

func messageWithMetadata(msgType event.MessageType, body string, metadata *KakaoMessageMetadata) *bridgev2.ConvertedMessage {
	converted := textMessage(msgType, body)
	converted.Parts[0].DBMetadata = metadata
	return converted
}

func convertPhoto(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg events.PhotoMessage) (*bridgev2.ConvertedMessage, error) {
	transferCtx, cancel := context.WithTimeout(ctx, matrixImageTransferTimeout)
	defer cancel()
	data, err := media.DownloadPhoto(transferCtx, photoHTTPClient, msg.Message.Attachment)
	if err != nil {
		if category, ok := deterministicPhotoFailure(err); ok {
			return photoConversionGapNotice(msg, category), nil
		}
		return nil, errPhotoTransfer
	}
	attachment := msg.Message.Attachment
	ext, mimeType := matrixPhotoType(attachment.MediaType)
	filename := "photo." + ext
	uri, file, err := intent.UploadMedia(transferCtx, portal.MXID, data, filename, mimeType)
	if err != nil {
		return nil, errPhotoTransfer
	}
	content := &event.MessageEventContent{MsgType: event.MsgImage, Body: filename, URL: uri, FileName: filename}
	if attachment.Comment != "" {
		content.Body = attachment.Comment
	}
	if file != nil {
		content.File = file
	}
	content.Info = &event.FileInfo{MimeType: mimeType, Size: len(data), Width: int(attachment.Width), Height: int(attachment.Height)}
	converted := &bridgev2.ConvertedMessage{Parts: []*bridgev2.ConvertedMessagePart{{Type: event.EventMessage, Content: content}}}
	converted.Parts[0].DBMetadata = newKakaoMessageMetadata(msg.Message.ChatID, msg.Message.LogID, msg.Message.AuthorID, media.PhotoType, "[image]", 0)
	return converted, nil
}

// matrixPhotoType maps a validated Kakao photo media type to a file extension
// and the registered MIME type. Kakao labels JPEG photos "image/jpg".
func matrixPhotoType(kakaoType string) (ext, mimeType string) {
	if kakaoType == "image/png" {
		return "png", "image/png"
	}
	return "jpg", "image/jpeg"
}

func deterministicPhotoFailure(err error) (string, bool) {
	switch {
	case errors.Is(err, media.ErrExpired):
		return "expired", true
	case errors.Is(err, media.ErrUnavailable):
		return "unavailable", true
	case errors.Is(err, media.ErrChecksumMismatch):
		return "checksum", true
	case errors.Is(err, media.ErrInvalidAttachment):
		return "invalid_attachment", true
	case errors.Is(err, media.ErrUnsupportedImage):
		return "unsupported_image", true
	case errors.Is(err, media.ErrUnsafeURL):
		return "unsafe_url", true
	default:
		return "", false
	}
}

func photoConversionGapNotice(msg events.PhotoMessage, category string) *bridgev2.ConvertedMessage {
	metadata := newKakaoMessageMetadata(msg.Message.ChatID, msg.Message.LogID, msg.Message.AuthorID, media.PhotoType, "[photo unavailable]", 0)
	metadata.ConversionGap = category
	return messageWithMetadata(event.MsgNotice, fmt.Sprintf("A KakaoTalk photo could not be displayed (%s).", category), metadata)
}

// GetChatInfo supplies the creation snapshot before bridgev2 creates a room.
// Ordinary membership changes continue using GetChatInfoChange.
func (evt *chatInfoChangeEvent) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	if ctx == nil {
		return nil, bridgev2.ErrNotLoggedIn
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return evt.getCreationInfo(ctx, portal)
}
