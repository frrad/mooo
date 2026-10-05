package connector

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/protocol/events"
)

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
		// Photo events carry no author or timestamp yet, so the notice comes
		// from the bridge bot (bridge plan B1).
		chatID, logID := evt.Message.ChatID, evt.Message.LogID
		return newMessage(kc.messageMeta(chatID, logID, 0, 0), makeMessageID(chatID, logID), "A photo was sent that this bridge cannot show yet.", convertNotice)
	case events.UnsupportedMessage:
		notice := fmt.Sprintf("A KakaoTalk message of unsupported type %d was sent.", evt.Type)
		return newMessage(kc.messageMeta(evt.ChatID, evt.LogID, 0, 0), makeMessageID(evt.ChatID, evt.LogID), notice, convertNotice)
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
	return textMessage(event.MsgText, msg.Message), nil
}

func convertReply(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg events.ReplyMessage) (*bridgev2.ConvertedMessage, error) {
	converted := textMessage(event.MsgText, msg.Message)
	if msg.Source.LogID > 0 {
		converted.ReplyTo = &networkid.MessageOptionalPartID{MessageID: makeMessageID(msg.ChatID, msg.Source.LogID)}
	}
	return converted, nil
}

func convertNotice(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, notice string) (*bridgev2.ConvertedMessage, error) {
	return textMessage(event.MsgNotice, notice), nil
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
