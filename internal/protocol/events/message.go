package events

import (
	"encoding/json"
	"fmt"
	"reflect"
	"unicode/utf8"

	"github.com/frrad/mooo/internal/protocol/bsonfield"
	"github.com/frrad/mooo/internal/protocol/chatlog"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// Position is the durable per-chat resume position of a message event.
type Position struct{ ChatID, LogID int64 }

type positioned interface{ Position() Position }

// MessagePosition returns the durable per-chat resume position carried by an
// incoming MSG event. Metadata and unknown packets are not message cursors.
func MessagePosition(event Event) (chatID, logID int64, ok bool) {
	p, ok := event.(positioned)
	if !ok || isNilPointer(event) {
		return 0, 0, false
	}
	pos := p.Position()
	return pos.ChatID, pos.LogID, true
}

func isNilPointer(event Event) bool {
	v := reflect.ValueOf(event)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

type TextMessage struct {
	ChatID   int64
	LogID    int64
	AuthorID int64
	SentAt   int64
	Message  string
	// Revision counts edits; an edited message arrives with revision >= 1.
	Revision int64
}

func (TextMessage) Kind() Kind           { return KindTextMessage }
func (m TextMessage) Position() Position { return Position{m.ChatID, m.LogID} }
func (TextMessage) isEvent()             {}

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

// ReplyAttachment is content a reply carries besides its text. Type is the
// attach_type message type (zero for a plain text reply); Sticker is set only
// when that content decodes as a supported sticker.
type ReplyAttachment struct {
	Type    int32
	Only    bool
	Sticker *media.StickerAttachment
}

type ReplyMessage struct {
	ChatID     int64
	LogID      int64
	AuthorID   int64
	SentAt     int64
	Message    string
	Source     ReplySource
	Attachment ReplyAttachment
}

func (ReplyMessage) Kind() Kind           { return KindReplyMessage }
func (m ReplyMessage) Position() Position { return Position{m.ChatID, m.LogID} }
func (ReplyMessage) isEvent()             {}

func (m ReplyMessage) String() string {
	return fmt.Sprintf("ReplyMessage{chatId=%d, logId=%d, authorId=%d, sourceLogId=%d, message=<redacted>, sourceMessage=<redacted>}", m.ChatID, m.LogID, m.AuthorID, m.Source.LogID)
}

func (m ReplyMessage) GoString() string { return m.String() }

type StickerMessage struct {
	ChatID, LogID, AuthorID, SentAt int64
	Type                            int32
	Attachment                      media.StickerAttachment
}

func (StickerMessage) Kind() Kind           { return KindStickerMessage }
func (m StickerMessage) Position() Position { return Position{m.ChatID, m.LogID} }
func (StickerMessage) isEvent()             {}
func (StickerMessage) String() string       { return "StickerMessage{<redacted>}" }
func (m StickerMessage) GoString() string   { return m.String() }

type ContactMessage struct{ Message media.ContactMessage }

func (ContactMessage) Kind() Kind { return KindContactMessage }
func (m ContactMessage) Position() Position {
	return Position{m.Message.ChatID, m.Message.LogID}
}
func (ContactMessage) isEvent()           {}
func (ContactMessage) String() string     { return "ContactMessage{<redacted>}" }
func (m ContactMessage) GoString() string { return m.String() }

type FileMessage struct{ Message media.FileMessage }

func (FileMessage) Kind() Kind { return KindFileMessage }
func (m FileMessage) Position() Position {
	return Position{m.Message.ChatID, m.Message.LogID}
}
func (FileMessage) isEvent()           {}
func (FileMessage) String() string     { return "FileMessage{<redacted>}" }
func (m FileMessage) GoString() string { return m.String() }

type AudioMessage struct{ Message media.AudioMessage }

func (AudioMessage) Kind() Kind { return KindAudioMessage }
func (m AudioMessage) Position() Position {
	return Position{m.Message.ChatID, m.Message.LogID}
}
func (AudioMessage) isEvent()           {}
func (AudioMessage) String() string     { return "AudioMessage{<redacted>}" }
func (m AudioMessage) GoString() string { return m.String() }

type VideoMessage struct{ Message media.VideoMessage }

func (VideoMessage) Kind() Kind { return KindVideoMessage }
func (m VideoMessage) Position() Position {
	return Position{m.Message.ChatID, m.Message.LogID}
}
func (VideoMessage) isEvent()           {}
func (VideoMessage) String() string     { return "VideoMessage{<redacted>}" }
func (m VideoMessage) GoString() string { return m.String() }

type MultiPhotoMessage struct{ Message media.MultiPhotoMessage }

func (MultiPhotoMessage) Kind() Kind { return KindMultiPhotoMessage }
func (m MultiPhotoMessage) Position() Position {
	return Position{m.Message.ChatID, m.Message.LogID}
}
func (MultiPhotoMessage) isEvent()           {}
func (MultiPhotoMessage) String() string     { return "MultiPhotoMessage{<redacted>}" }
func (m MultiPhotoMessage) GoString() string { return m.String() }

type PhotoMessage struct {
	Message media.PhotoMessage
}

func (PhotoMessage) Kind() Kind { return KindPhotoMessage }
func (m PhotoMessage) Position() Position {
	return Position{m.Message.ChatID, m.Message.LogID}
}
func (PhotoMessage) isEvent() {}

func (m PhotoMessage) String() string {
	return fmt.Sprintf("PhotoMessage{chatId=%d, logId=%d, attachment=<redacted>}", m.Message.ChatID, m.Message.LogID)
}

func (m PhotoMessage) GoString() string { return m.String() }

type UnsupportedMessage struct {
	ChatID int64
	LogID  int64
	Type   int32
}

func (UnsupportedMessage) Kind() Kind           { return KindUnsupportedMessage }
func (m UnsupportedMessage) Position() Position { return Position{m.ChatID, m.LogID} }
func (UnsupportedMessage) isEvent()             {}

func decodeMessage(packet loco.Packet) (Event, error) {
	env, err := chatlog.Decode(packet.Body)
	if err != nil {
		return nil, ErrMalformedEvent
	}
	if env.Type == messagetype.Feed {
		return decodeFeed(env.ChatID, env.LogID, env.Log)
	}
	if deletedFlagged(env.Type) {
		return DeletedMessage{ChatID: env.ChatID, LogID: env.LogID, AuthorID: env.AuthorID, SentAt: env.SentAt, BaseType: env.Type &^ messagetype.DeletedAllChatTypeFlag}, nil
	}
	if decode, ok := messageDecoders[env.Type]; ok {
		return decode(env)
	}
	return UnsupportedMessage{ChatID: env.ChatID, LogID: env.LogID, Type: env.Type}, nil
}

// messageDecoders maps each supported chatLog.type to its content decoder.
var messageDecoders = map[int32]func(chatlog.Envelope) (Event, error){
	messagetype.Text:            decodeText,
	messagetype.Reply:           logDecoder(decodeReply),
	messagetype.Post:            logDecoder(decodePost),
	messagetype.Vote:            logDecoder(decodeVote),
	messagetype.Location:        logDecoder(decodeLocation),
	messagetype.Profile:         logDecoder(decodeProfile),
	messagetype.Mvoip:           decodeCall,
	messagetype.Leverage:        decodeLeverage,
	messagetype.Sticker:         decodeSticker,
	messagetype.AnimatedSticker: decodeSticker,
	messagetype.Photo:           mediaDecoder(media.DecodePhotoMessage, func(m media.PhotoMessage) Event { return PhotoMessage{Message: m} }),
	messagetype.Contact:         mediaDecoder(media.DecodeContactMessage, func(m media.ContactMessage) Event { return ContactMessage{Message: m} }),
	messagetype.File:            mediaDecoder(media.DecodeFileMessage, func(m media.FileMessage) Event { return FileMessage{Message: m} }),
	messagetype.Audio:           mediaDecoder(media.DecodeAudioMessage, func(m media.AudioMessage) Event { return AudioMessage{Message: m} }),
	messagetype.Video:           mediaDecoder(media.DecodeVideoMessage, func(m media.VideoMessage) Event { return VideoMessage{Message: m} }),
	messagetype.MultiPhoto:      mediaDecoder(media.DecodeMultiPhotoMessage, func(m media.MultiPhotoMessage) Event { return MultiPhotoMessage{Message: m} }),
}

// logDecoder adapts a decoder that reads its content from the chatLog document.
func logDecoder(decode func(chatID, logID int64, log bson.Raw) (Event, error)) func(chatlog.Envelope) (Event, error) {
	return func(env chatlog.Envelope) (Event, error) { return decode(env.ChatID, env.LogID, env.Log) }
}

// mediaDecoder adapts a media package decoder. Every media error is malformed;
// the media error stays in the chain so a gap can report its reason.
func mediaDecoder[M any](decode func(chatlog.Envelope) (M, error), wrap func(M) Event) func(chatlog.Envelope) (Event, error) {
	return func(env chatlog.Envelope) (Event, error) {
		message, err := decode(env)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformedEvent, err)
		}
		return wrap(message), nil
	}
}

func decodeText(env chatlog.Envelope) (Event, error) {
	// Accept the write-side field name msg for compatible synthetic backends.
	message, ok, err := fields.String(env.Log, "message", "msg")
	if err != nil || !ok || !utf8.ValidString(message) {
		return nil, ErrMalformedEvent
	}
	return decodeMiniText(TextMessage{
		ChatID: env.ChatID, LogID: env.LogID, Message: message,
		AuthorID: env.AuthorID, SentAt: env.SentAt,
		Revision: bsonfield.Int64OrZero(env.Log, "revision"),
	}, env.Log)
}

func decodeSticker(env chatlog.Envelope) (Event, error) {
	value, err := requiredString(env.Log, "attachment")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	attachment, err := media.DecodeStickerAttachment(value)
	if err != nil {
		return nil, ErrMalformedEvent
	}
	return StickerMessage{ChatID: env.ChatID, LogID: env.LogID, AuthorID: env.AuthorID, SentAt: env.SentAt, Type: env.Type, Attachment: attachment}, nil
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
	var attached struct {
		Only    bool            `json:"attach_only"`
		Type    int32           `json:"attach_type"`
		Content json.RawMessage `json:"attach_content"`
	}
	if err := decodeSingleJSON(attachment, &attached); err != nil || attached.Type < 0 {
		return nil, ErrMalformedEvent
	}
	replyAttachment := ReplyAttachment{Type: attached.Type, Only: attached.Only}
	if attached.Type == int32(messagetype.Sticker) || attached.Type == int32(messagetype.AnimatedSticker) {
		// A sticker that does not decode stays an unsupported attachment
		// marker; the reply itself remains deliverable.
		if sticker, err := media.DecodeStickerAttachment(string(attached.Content)); err == nil {
			replyAttachment.Sticker = &sticker
		}
	}
	return ReplyMessage{
		ChatID: chatID, LogID: logID, AuthorID: bsonfield.Int64OrZero(chatLog, "authorId"),
		SentAt: bsonfield.Int64OrZero(chatLog, "sendAt"), Message: message,
		Source:     ReplySource{LogID: source.LogID, UserID: source.UserID, LinkID: source.LinkID, Type: source.Type, Message: source.Message},
		Attachment: replyAttachment,
	}, nil
}
