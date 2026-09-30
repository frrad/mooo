// Package events decodes unsolicited LOCO packets into stable client events.
package events

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/media"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type Kind string

const (
	KindTextMessage        Kind = "text_message"
	KindReplyMessage       Kind = "reply_message"
	KindPhotoMessage       Kind = "photo_message"
	KindUnsupportedMessage Kind = "unsupported_message"
	KindReactionChanged    Kind = "reaction_changed"
	KindUnsupportedLogMeta Kind = "unsupported_log_meta"
	KindUnknownPacket      Kind = "unknown_packet"
)

var ErrMalformedEvent = errors.New("events: malformed event")

// Event is implemented by every typed unsolicited event.
type Event interface {
	Kind() Kind
	isEvent()
}

// Result carries one decoded event or one non-fatal packet decoding error.
// A malformed packet does not terminate the surrounding event stream.
type Result struct {
	Event Event
	Err   error
}

// MessagePosition returns the durable per-chat resume position carried by an
// incoming MSG event. Metadata and unknown packets are not message cursors.
func MessagePosition(event Event) (chatID, logID int64, ok bool) {
	switch value := event.(type) {
	case TextMessage:
		return value.ChatID, value.LogID, true
	case *TextMessage:
		if value != nil {
			return value.ChatID, value.LogID, true
		}
	case ReplyMessage:
		return value.ChatID, value.LogID, true
	case *ReplyMessage:
		if value != nil {
			return value.ChatID, value.LogID, true
		}
	case PhotoMessage:
		return value.Message.ChatID, value.Message.LogID, true
	case *PhotoMessage:
		if value == nil {
			return 0, 0, false
		}
		return value.Message.ChatID, value.Message.LogID, true
	case UnsupportedMessage:
		return value.ChatID, value.LogID, true
	case *UnsupportedMessage:
		if value != nil {
			return value.ChatID, value.LogID, true
		}
	default:
		return 0, 0, false
	}
	return 0, 0, false
}

type TextMessage struct {
	ChatID   int64
	LogID    int64
	AuthorID int64
	SentAt   int64
	Message  string
}

func (TextMessage) Kind() Kind { return KindTextMessage }
func (TextMessage) isEvent()   {}

func (m TextMessage) String() string {
	return fmt.Sprintf("TextMessage{chatId=%d, logId=%d, authorId=%d, message=<redacted>}", m.ChatID, m.LogID, m.AuthorID)
}

func (m TextMessage) GoString() string { return m.String() }

type ReplySource struct {
	LogID   int64
	UserID  int64
	LinkID  int64
	Type    int32
	Message string
}

type ReplyMessage struct {
	ChatID   int64
	LogID    int64
	AuthorID int64
	SentAt   int64
	Message  string
	Source   ReplySource
}

func (ReplyMessage) Kind() Kind { return KindReplyMessage }
func (ReplyMessage) isEvent()   {}

func (m ReplyMessage) String() string {
	return fmt.Sprintf("ReplyMessage{chatId=%d, logId=%d, authorId=%d, sourceLogId=%d, message=<redacted>, sourceMessage=<redacted>}", m.ChatID, m.LogID, m.AuthorID, m.Source.LogID)
}

func (m ReplyMessage) GoString() string { return m.String() }

type PhotoMessage struct {
	Message media.PhotoMessage
}

func (PhotoMessage) Kind() Kind { return KindPhotoMessage }
func (PhotoMessage) isEvent()   {}

func (m PhotoMessage) String() string {
	return fmt.Sprintf("PhotoMessage{chatId=%d, logId=%d, attachment=<redacted>}", m.Message.ChatID, m.Message.LogID)
}

func (m PhotoMessage) GoString() string { return m.String() }

type UnsupportedMessage struct {
	ChatID int64
	LogID  int64
	Type   int32
}

func (UnsupportedMessage) Kind() Kind { return KindUnsupportedMessage }
func (UnsupportedMessage) isEvent()   {}

type ReactionItem struct {
	ID    string
	Kind  int64
	Count int64
	Alt   map[string]string
}

type ReactionChanged struct {
	ChatID   int64
	LinkID   int64
	LogID    int64
	Revision int64
	Items    []ReactionItem
}

func (ReactionChanged) Kind() Kind { return KindReactionChanged }
func (ReactionChanged) isEvent()   {}

func (m ReactionChanged) String() string {
	return fmt.Sprintf("ReactionChanged{chatId=%d, logId=%d, revision=%d, items=%d, content=<redacted>}", m.ChatID, m.LogID, m.Revision, len(m.Items))
}

func (m ReactionChanged) GoString() string { return m.String() }

type UnsupportedLogMeta struct {
	ChatID int64
	LogID  int64
	Type   int32
}

func (UnsupportedLogMeta) Kind() Kind { return KindUnsupportedLogMeta }
func (UnsupportedLogMeta) isEvent()   {}

type UnknownPacket struct {
	Method string
}

func (UnknownPacket) Kind() Kind { return KindUnknownPacket }
func (UnknownPacket) isEvent()   {}

// Decode turns one unsolicited packet into a typed event. Unknown packet
// methods and unsupported message types remain observable without exposing raw
// account data. Malformed known packets return ErrMalformedEvent.
func Decode(packet loco.Packet) (Event, error) {
	switch packet.Header.Method {
	case "MSG":
		return decodeMessage(packet)
	case "CHGLOGMETA":
		return decodeLogMeta(packet.Body)
	default:
		return UnknownPacket{Method: packet.Header.Method}, nil
	}
}

func decodeMessage(packet loco.Packet) (Event, error) {
	raw := bson.Raw(packet.Body)
	chatID, logID, messageType, chatLog, err := messageEnvelope(raw)
	if err != nil {
		return nil, ErrMalformedEvent
	}
	switch messageType {
	case 1:
		messageValue, err := chatLog.LookupErr("message")
		if err != nil {
			// Accept the write-side field name for compatible synthetic backends.
			messageValue, err = chatLog.LookupErr("msg")
		}
		if err != nil || messageValue.Type != bson.TypeString || !utf8.ValidString(messageValue.StringValue()) {
			return nil, ErrMalformedEvent
		}
		return TextMessage{
			ChatID: chatID, LogID: logID, Message: messageValue.StringValue(),
			AuthorID: optionalInt64(chatLog, "authorId"), SentAt: optionalInt64(chatLog, "sendAt"),
		}, nil
	case media.PhotoType:
		photo, err := media.DecodePhotoMessage(packet.Body)
		if err != nil {
			return nil, ErrMalformedEvent
		}
		return PhotoMessage{Message: photo}, nil
	case chat.ReplyType:
		return decodeReply(chatID, logID, chatLog)
	default:
		return UnsupportedMessage{ChatID: chatID, LogID: logID, Type: messageType}, nil
	}
}

func decodeReply(chatID, logID int64, chatLog bson.Raw) (Event, error) {
	message, err := requiredString(chatLog, "message")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	attachment, err := requiredString(chatLog, "attachment")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	var source struct {
		LogID   int64  `json:"src_logId"`
		UserID  int64  `json:"src_userId"`
		LinkID  int64  `json:"src_linkId"`
		Type    int32  `json:"src_type"`
		Message string `json:"src_message"`
	}
	if err := decodeSingleJSON(attachment, &source); err != nil || source.LogID <= 0 || source.UserID <= 0 || source.LinkID < 0 || source.Type <= 0 || !validEventString(source.Message) {
		return nil, ErrMalformedEvent
	}
	return ReplyMessage{
		ChatID: chatID, LogID: logID, AuthorID: optionalInt64(chatLog, "authorId"),
		SentAt: optionalInt64(chatLog, "sendAt"), Message: message,
		Source: ReplySource{LogID: source.LogID, UserID: source.UserID, LinkID: source.LinkID, Type: source.Type, Message: source.Message},
	}, nil
}

func decodeLogMeta(body []byte) (Event, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil {
		return nil, ErrMalformedEvent
	}
	chatID, err := requiredInt64(raw, "chatId")
	if err != nil || chatID <= 0 {
		return nil, ErrMalformedEvent
	}
	logID, err := requiredInt64(raw, "logId")
	if err != nil || logID <= 0 {
		return nil, ErrMalformedEvent
	}
	metaType, err := requiredInt64(raw, "type")
	if err != nil || metaType <= 0 || metaType > int64(^uint32(0)>>1) {
		return nil, ErrMalformedEvent
	}
	if metaType != 2 {
		return UnsupportedLogMeta{ChatID: chatID, LogID: logID, Type: int32(metaType)}, nil
	}
	revision, err := requiredInt64(raw, "revision")
	if err != nil || revision <= 0 {
		return nil, ErrMalformedEvent
	}
	linkID := optionalInt64(raw, "linkId")
	if linkID < 0 {
		return nil, ErrMalformedEvent
	}
	content, err := requiredString(raw, "content")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	var payload struct {
		Reactions []struct {
			Alt   map[string]string `json:"a"`
			Count int64             `json:"c"`
			Kind  int64             `json:"k"`
			ID    string            `json:"o"`
		} `json:"rx"`
	}
	if err := decodeSingleJSON(content, &payload); err != nil || payload.Reactions == nil {
		return nil, ErrMalformedEvent
	}
	items := make([]ReactionItem, 0, len(payload.Reactions))
	for _, item := range payload.Reactions {
		if item.ID == "" || item.Count < 0 || item.Kind < 0 {
			return nil, ErrMalformedEvent
		}
		items = append(items, ReactionItem{ID: item.ID, Kind: item.Kind, Count: item.Count, Alt: item.Alt})
	}
	return ReactionChanged{ChatID: chatID, LinkID: linkID, LogID: logID, Revision: revision, Items: items}, nil
}

func messageEnvelope(raw bson.Raw) (int64, int64, int32, bson.Raw, error) {
	if err := raw.Validate(); err != nil {
		return 0, 0, 0, nil, err
	}
	chatID, err := requiredInt64(raw, "chatId")
	if err != nil || chatID <= 0 {
		return 0, 0, 0, nil, ErrMalformedEvent
	}
	logValue, err := raw.LookupErr("chatLog")
	if err != nil || logValue.Type != bson.TypeEmbeddedDocument {
		return 0, 0, 0, nil, ErrMalformedEvent
	}
	chatLog := logValue.Document()
	logID, err := requiredInt64(chatLog, "logId")
	if err != nil {
		logID, err = requiredInt64(raw, "logId")
	}
	if err != nil || logID <= 0 {
		return 0, 0, 0, nil, ErrMalformedEvent
	}
	typeValue, err := requiredInt64(chatLog, "type")
	if err != nil || typeValue <= 0 || typeValue > int64(^uint32(0)>>1) {
		return 0, 0, 0, nil, ErrMalformedEvent
	}
	return chatID, logID, int32(typeValue), chatLog, nil
}

func requiredInt64(raw bson.Raw, key string) (int64, error) {
	value, err := raw.LookupErr(key)
	if err != nil {
		return 0, err
	}
	switch value.Type {
	case bson.TypeInt64:
		return value.Int64(), nil
	case bson.TypeInt32:
		return int64(value.Int32()), nil
	default:
		return 0, ErrMalformedEvent
	}
}

func optionalInt64(raw bson.Raw, key string) int64 {
	value, err := requiredInt64(raw, key)
	if err != nil {
		return 0
	}
	return value
}

func requiredString(raw bson.Raw, key string) (string, error) {
	value, err := raw.LookupErr(key)
	if err != nil || value.Type != bson.TypeString || !validEventString(value.StringValue()) {
		return "", ErrMalformedEvent
	}
	return value.StringValue(), nil
}

func decodeSingleJSON(value string, target any) error {
	decoder := json.NewDecoder(bytes.NewBufferString(value))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return ErrMalformedEvent
	}
	return nil
}

func validEventString(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}
