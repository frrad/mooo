package connector

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

var errPhotoTransfer = errors.New("connector: Kakao photo transfer failed")

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
	case events.TextMessage:
		return newMessage(kc.messageMeta(evt.ChatID, evt.LogID, evt.AuthorID, evt.SentAt), makeMessageID(evt.ChatID, evt.LogID), evt, convertText)
	case events.ReplyMessage:
		return newMessage(kc.messageMeta(evt.ChatID, evt.LogID, evt.AuthorID, evt.SentAt), makeMessageID(evt.ChatID, evt.LogID), evt, convertReply)
	case events.StickerMessage:
		return newMessage(kc.messageMeta(evt.ChatID, evt.LogID, evt.AuthorID, evt.SentAt), makeMessageID(evt.ChatID, evt.LogID), evt, convertSticker)
	case events.VideoMessage:
		a := evt.Message
		return newMessage(kc.messageMeta(a.ChatID, a.LogID, a.AuthorID, a.SentAt), makeMessageID(a.ChatID, a.LogID), evt, convertVideo)
	case events.MultiPhotoMessage:
		return kc.albumEvent(evt)
	case events.PhotoMessage:
		chatID, logID := evt.Message.ChatID, evt.Message.LogID
		return newMessage(kc.messageMeta(chatID, logID, evt.Message.AuthorID, evt.Message.SentAt), makeMessageID(chatID, logID), evt, convertPhoto)
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
				Sender:    kc.selfSender(),
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
			Type:      bridgev2.RemoteEventChatInfoChange,
			PortalKey: portalKey,
			Sender:    kc.senderFor(authorID),
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Int64("kakao_chat_id", chatID).Int64("kakao_log_id", logID)
			},
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
	getInfo    func(context.Context) (*bridgev2.ChatInfo, error)
	getChanges func(context.Context) (*bridgev2.ChatMemberList, error)
}

var _ bridgev2.RemoteChatInfoChange = (*chatInfoChangeEvent)(nil)

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
		if identity.UserID <= 0 || identity.UserID == kc.userID {
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
			members.Set(bridgev2.ChatMember{EventSender: bridgev2.EventSender{Sender: makeUserID(userID)}, Membership: event.MembershipLeave})
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
	return &simplevent.Message[T]{
		EventMeta:          meta,
		ID:                 id,
		Data:               data,
		ConvertMessageFunc: convert,
	}
}

func (kc *KakaoClient) messageMeta(chatID, logID, authorID, sentAt int64) simplevent.EventMeta {
	return simplevent.EventMeta{
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
	return messageWithMetadata(event.MsgText, msg.Message, newKakaoMessageMetadata(msg.ChatID, msg.LogID, msg.AuthorID, chat.TextType, msg.Message, 0)), nil
}

func convertReply(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg events.ReplyMessage) (*bridgev2.ConvertedMessage, error) {
	converted := messageWithMetadata(event.MsgText, msg.Message, newKakaoMessageMetadata(msg.ChatID, msg.LogID, msg.AuthorID, chat.ReplyType, msg.Message, 0))
	if msg.Source.LogID > 0 {
		converted.ReplyTo = &networkid.MessageOptionalPartID{MessageID: makeMessageID(msg.ChatID, msg.Source.LogID)}
	}
	return converted, nil
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
	filename := "photo.jpg"
	if attachment.MediaType == "image/png" {
		filename = "photo.png"
	}
	uri, file, err := intent.UploadMedia(transferCtx, portal.MXID, data, filename, attachment.MediaType)
	if err != nil {
		return nil, errPhotoTransfer
	}
	content := &event.MessageEventContent{MsgType: event.MsgImage, Body: filename, URL: uri, FileName: filename}
	if file != nil {
		content.File = file
	}
	content.Info = &event.FileInfo{MimeType: attachment.MediaType, Size: len(data), Width: int(attachment.Width), Height: int(attachment.Height)}
	converted := &bridgev2.ConvertedMessage{Parts: []*bridgev2.ConvertedMessagePart{{Type: event.EventMessage, Content: content}}}
	converted.Parts[0].DBMetadata = newKakaoMessageMetadata(msg.Message.ChatID, msg.Message.LogID, msg.Message.AuthorID, media.PhotoType, "[image]", 0)
	return converted, nil
}

func deterministicPhotoFailure(err error) (string, bool) {
	switch {
	case errors.Is(err, media.ErrExpired):
		return "expired", true
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
