// Package events decodes unsolicited LOCO packets into stable client events.
package events

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/media"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type Kind string

const (
	KindTextMessage        Kind = "text_message"
	KindPhotoMessage       Kind = "photo_message"
	KindUnsupportedMessage Kind = "unsupported_message"
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

type UnknownPacket struct {
	Method string
}

func (UnknownPacket) Kind() Kind { return KindUnknownPacket }
func (UnknownPacket) isEvent()   {}

// Decode turns one unsolicited packet into a typed event. Unknown packet
// methods and unsupported message types remain observable without exposing raw
// account data. Malformed known packets return ErrMalformedEvent.
func Decode(packet loco.Packet) (Event, error) {
	if packet.Header.Method != "MSG" {
		return UnknownPacket{Method: packet.Header.Method}, nil
	}
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
	default:
		return UnsupportedMessage{ChatID: chatID, LogID: logID, Type: messageType}, nil
	}
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
