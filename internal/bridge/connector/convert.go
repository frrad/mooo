package connector

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
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
	case events.PhotoMessage:
		chatID, logID := evt.Message.ChatID, evt.Message.LogID
		return newMessage(kc.messageMeta(chatID, logID, evt.Message.AuthorID, evt.Message.SentAt), makeMessageID(chatID, logID), evt, convertPhoto)
	case events.UnsupportedMessage:
		notice := fmt.Sprintf("A KakaoTalk message of unsupported type %d was sent.", evt.Type)
		return newMessage(kc.messageMeta(evt.ChatID, evt.LogID, 0, 0), makeMessageID(evt.ChatID, evt.LogID), noticeData{
			Body: notice, Metadata: newKakaoMessageMetadata(evt.ChatID, evt.LogID, 0, evt.Type, "", 0),
		}, convertNoticeWithMetadata)
	default:
		return nil
	}
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
