// Package events decodes unsolicited LOCO packets into stable client events.
package events

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type Kind string

const (
	KindTextMessage        Kind = "text_message"
	KindPostMessage        Kind = "post_message"
	KindVoteMessage        Kind = "vote_message"
	KindLocationMessage    Kind = "location_message"
	KindProfileMessage     Kind = "profile_message"
	KindReplyMessage       Kind = "reply_message"
	KindPhotoMessage       Kind = "photo_message"
	KindVideoMessage       Kind = "video_message"
	KindAudioMessage       Kind = "audio_message"
	KindFileMessage        Kind = "file_message"
	KindContactMessage     Kind = "contact_message"
	KindMultiPhotoMessage  Kind = "multi_photo_message"
	KindStickerMessage     Kind = "sticker_message"
	KindMessageGap         Kind = "message_gap"
	KindUnsupportedMessage Kind = "unsupported_message"
	KindReactionChanged    Kind = "reaction_changed"
	KindReadStateChanged   Kind = "read_state_changed"
	KindChangeServer       Kind = "change_server"
	KindKickout            Kind = "kickout"
	KindMemberRemoved      Kind = "member_removed"
	KindMemberAdded        Kind = "member_added"
	KindChatLeft           Kind = "chat_left"
	KindChatStatusChanged  Kind = "chat_status_changed"
	KindChatMetaChanged    Kind = "chat_meta_changed"
	KindChatMCMetaChanged  Kind = "chat_mcmeta_changed"
	KindUnsupportedLogMeta Kind = "unsupported_log_meta"
	KindUnknownPacket      Kind = "unknown_packet"
)

var ErrMalformedEvent = errors.New("events: malformed event")

// ErrUnidentifiableMessage means a MSG packet could not be admitted because
// its chat/log identity could not be established unambiguously. Callers must
// stop delivery rather than inventing a cursor position or silently skip it.
// It wraps ErrMalformedEvent so strict parser callers retain their broad
// malformed-event classification while delivery callers can distinguish this
// continuity-breaking case.
var ErrUnidentifiableMessage = fmt.Errorf("%w: message identity unavailable", ErrMalformedEvent)

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
	case PostMessage:
		return value.ChatID, value.LogID, true
	case *PostMessage:
		if value != nil {
			return value.ChatID, value.LogID, true
		}
	case VoteMessage:
		return value.ChatID, value.LogID, true
	case *VoteMessage:
		if value != nil {
			return value.ChatID, value.LogID, true
		}
	case MessageGap:
		return value.ChatID, value.LogID, true
	case *MessageGap:
		if value != nil {
			return value.ChatID, value.LogID, true
		}
	case LocationMessage:
		return value.ChatID, value.LogID, true
	case *LocationMessage:
		if value != nil {
			return value.ChatID, value.LogID, true
		}
	case ProfileMessage:
		return value.ChatID, value.LogID, true
	case *ProfileMessage:
		if value != nil {
			return value.ChatID, value.LogID, true
		}
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
	case ContactMessage:
		return value.Message.ChatID, value.Message.LogID, true
	case *ContactMessage:
		if value != nil {
			return value.Message.ChatID, value.Message.LogID, true
		}
	case FileMessage:
		return value.Message.ChatID, value.Message.LogID, true
	case *FileMessage:
		if value != nil {
			return value.Message.ChatID, value.Message.LogID, true
		}
	case AudioMessage:
		return value.Message.ChatID, value.Message.LogID, true
	case *AudioMessage:
		if value != nil {
			return value.Message.ChatID, value.Message.LogID, true
		}
		return 0, 0, false
	case VideoMessage:
		return value.Message.ChatID, value.Message.LogID, true
	case *VideoMessage:
		if value != nil {
			return value.Message.ChatID, value.Message.LogID, true
		}
	case MultiPhotoMessage:
		return value.Message.ChatID, value.Message.LogID, true
	case *MultiPhotoMessage:
		if value != nil {
			return value.Message.ChatID, value.Message.LogID, true
		}
	case StickerMessage:
		return value.ChatID, value.LogID, true
	case *StickerMessage:
		if value != nil {
			return value.ChatID, value.LogID, true
		}
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

type StickerMessage struct {
	ChatID, LogID, AuthorID, SentAt int64
	Type                            int32
	Attachment                      media.StickerAttachment
}

func (StickerMessage) Kind() Kind         { return KindStickerMessage }
func (StickerMessage) isEvent()           {}
func (StickerMessage) String() string     { return "StickerMessage{<redacted>}" }
func (m StickerMessage) GoString() string { return m.String() }

type ContactMessage struct{ Message media.ContactMessage }

func (ContactMessage) Kind() Kind         { return KindContactMessage }
func (ContactMessage) isEvent()           {}
func (ContactMessage) String() string     { return "ContactMessage{<redacted>}" }
func (m ContactMessage) GoString() string { return m.String() }

type FileMessage struct{ Message media.FileMessage }

func (FileMessage) Kind() Kind         { return KindFileMessage }
func (FileMessage) isEvent()           {}
func (FileMessage) String() string     { return "FileMessage{<redacted>}" }
func (m FileMessage) GoString() string { return m.String() }

type AudioMessage struct{ Message media.AudioMessage }

func (AudioMessage) Kind() Kind         { return KindAudioMessage }
func (AudioMessage) isEvent()           {}
func (AudioMessage) String() string     { return "AudioMessage{<redacted>}" }
func (m AudioMessage) GoString() string { return m.String() }

type VideoMessage struct{ Message media.VideoMessage }

func (VideoMessage) Kind() Kind         { return KindVideoMessage }
func (VideoMessage) isEvent()           {}
func (VideoMessage) String() string     { return "VideoMessage{<redacted>}" }
func (m VideoMessage) GoString() string { return m.String() }

type MultiPhotoMessage struct{ Message media.MultiPhotoMessage }

func (MultiPhotoMessage) Kind() Kind         { return KindMultiPhotoMessage }
func (MultiPhotoMessage) isEvent()           {}
func (MultiPhotoMessage) String() string     { return "MultiPhotoMessage{<redacted>}" }
func (m MultiPhotoMessage) GoString() string { return m.String() }

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
	MetadataType int32
	ChatID       int64
	LinkID       int64
	LogID        int64
	Revision     int64
	Items        []ReactionItem
}

func (ReactionChanged) Kind() Kind { return KindReactionChanged }
func (ReactionChanged) isEvent()   {}

func (m ReactionChanged) String() string {
	return fmt.Sprintf("ReactionChanged{chatId=%d, logId=%d, revision=%d, items=%d, content=<redacted>}", m.ChatID, m.LogID, m.Revision, len(m.Items))
}

func (m ReactionChanged) GoString() string { return m.String() }

// ReadStateChanged reports that one room member's server watermark advanced.
// It does not imply that the local application should mark the conversation
// read; UserID identifies whose watermark changed.
type ReadStateChanged struct {
	ChatID    int64
	UserID    int64
	Watermark int64
}

func (ReadStateChanged) Kind() Kind { return KindReadStateChanged }
func (ReadStateChanged) isEvent()   {}

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

// ChangeServer reports a server-directed route-change notice. Its payload is
// intentionally empty at this decoder boundary; lifecycle effects belong to
// the owning manager.
type ChangeServer struct{}

func (ChangeServer) Kind() Kind { return KindChangeServer }
func (ChangeServer) isEvent()   {}

// Kickout reports a server-directed session termination notice. Reason is the
// signed int32 value supplied by the server, or zero when it is absent.
type Kickout struct {
	Reason int32
}

func (Kickout) Kind() Kind { return KindKickout }
func (Kickout) isEvent()   {}

// MemberRemoved identifies the departed member carried by a DELMEM notice.
// It is a typed event only; persistence and room mutation remain separate.
type MemberRemoved struct {
	ChatID   int64
	LogID    int64
	UserID   int64
	UserType int32
}

func (MemberRemoved) Kind() Kind { return KindMemberRemoved }
func (MemberRemoved) isEvent()   {}

type MemberIdentity struct {
	UserID   int64
	UserType int32
}

// MemberAdded identifies invitees carried by a NEWMEM notice. It is a typed
// event only; persistence and room mutation remain separate.
type MemberAdded struct {
	ChatID  int64
	LogID   int64
	Members []MemberIdentity
}

func (MemberAdded) Kind() Kind { return KindMemberAdded }
func (MemberAdded) isEvent()   {}

// ChatLeft carries the chat and cursor identity from a LEFT notice. Room
// deletion and cursor persistence remain manager-owned effects.
type ChatLeft struct {
	ChatID      int64
	LastTokenID int64
}

func (ChatLeft) Kind() Kind { return KindChatLeft }
func (ChatLeft) isEvent()   {}

// ChatStatusChanged carries the typed identity and opaque status document from
// a CHGCHATST notice. Status interpretation and lifecycle effects remain
// outside the decoder boundary.
type ChatStatusChanged struct {
	ChatID     int64
	PlusUserID int64
	Revision   int64
	Status     bson.Raw
}

func (ChatStatusChanged) Kind() Kind { return KindChatStatusChanged }
func (ChatStatusChanged) isEvent()   {}

// ChatMetaChanged carries the proven CHGMETA fields without interpreting the
// numeric subtype or applying metadata persistence/lifecycle effects.
type ChatMetaChanged struct {
	ChatID    int64
	Type      int32
	Revision  int64
	AuthorID  int64
	Content   string
	UpdatedAt int64
}

func (ChatMetaChanged) Kind() Kind { return KindChatMetaChanged }
func (ChatMetaChanged) isEvent()   {}

// ChatMCMetaChanged carries the decoder-proven MCM fields without interpreting
// type labels or applying room/revision effects.
type ChatMCMetaChanged struct {
	ChatID       int64
	Revision     int32
	Type         string
	Content      string
	ImageURL     string
	FullImageURL string
}

func (ChatMCMetaChanged) Kind() Kind { return KindChatMCMetaChanged }
func (ChatMCMetaChanged) isEvent()   {}

// Decode turns one unsolicited packet into a typed event. Unknown packet
// methods and unsupported message types remain observable without exposing raw
// account data. Malformed known packets return ErrMalformedEvent.
func Decode(packet loco.Packet) (Event, error) {
	switch packet.Header.Method {
	case "MSG":
		return decodeMessage(packet)
	case "CHGLOGMETA":
		return decodeLogMeta(packet.Body)
	case "DECUNREAD":
		return decodeReadState(packet.Body)
	case "CHANGESVR":
		return decodeChangeServer(packet.Body)
	case "KICKOUT":
		return decodeKickout(packet.Body)
	case "DELMEM":
		return decodeMemberRemoved(packet.Body)
	case "NEWMEM":
		return decodeMemberAdded(packet.Body)
	case "CHGCHATST":
		return decodeChatStatusChanged(packet.Body)
	case "CHGMETA":
		return decodeChatMetaChanged(packet.Body)
	case "CHGMCMETA":
		return decodeChatMCMetaChanged(packet.Body)
	case "LEFT":
		return decodeChatLeft(packet.Body)
	default:
		return UnknownPacket{Method: packet.Header.Method}, nil
	}
}

func decodeChangeServer(body []byte) (Event, error) {
	if err := bson.Raw(body).Validate(); err != nil {
		return nil, ErrMalformedEvent
	}
	return ChangeServer{}, nil
}

func decodeKickout(body []byte) (Event, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil {
		return nil, ErrMalformedEvent
	}
	reason := int32(0)
	value, err := raw.LookupErr("reason")
	if err == nil {
		if value.Type != bson.TypeInt32 {
			return nil, ErrMalformedEvent
		}
		reason = value.Int32()
	}
	return Kickout{Reason: reason}, nil
}

func decodeMemberRemoved(body []byte) (Event, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil {
		return nil, ErrMalformedEvent
	}
	chatLogValue, err := raw.LookupErr("chatLog")
	if err != nil || chatLogValue.Type != bson.TypeEmbeddedDocument {
		return nil, ErrMalformedEvent
	}
	chatLog := chatLogValue.Document()
	chatID, err := exactInt64(chatLog, "chatId")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	logID, err := exactInt64(chatLog, "logId")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	feedValue, err := chatLog.LookupErr("feed")
	if err != nil || feedValue.Type != bson.TypeEmbeddedDocument {
		return nil, ErrMalformedEvent
	}
	leaverValue, err := feedValue.Document().LookupErr("leaver")
	if err != nil || leaverValue.Type != bson.TypeEmbeddedDocument {
		return nil, ErrMalformedEvent
	}
	leaver := leaverValue.Document()
	userID, err := exactInt64(leaver, "userId")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	userTypeValue, err := leaver.LookupErr("userType")
	if err != nil || userTypeValue.Type != bson.TypeInt32 {
		return nil, ErrMalformedEvent
	}
	return MemberRemoved{ChatID: chatID, LogID: logID, UserID: userID, UserType: userTypeValue.Int32()}, nil
}

func decodeMemberAdded(body []byte) (Event, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil {
		return nil, ErrMalformedEvent
	}
	chatLogValue, err := raw.LookupErr("chatLog")
	if err != nil || chatLogValue.Type != bson.TypeEmbeddedDocument {
		return nil, ErrMalformedEvent
	}
	chatLog := chatLogValue.Document()
	chatID, err := exactInt64(chatLog, "chatId")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	logID, err := exactInt64(chatLog, "logId")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	feedValue, err := chatLog.LookupErr("feed")
	if err != nil || feedValue.Type != bson.TypeEmbeddedDocument {
		return nil, ErrMalformedEvent
	}
	inviteesValue, err := feedValue.Document().LookupErr("invitees")
	if err != nil || inviteesValue.Type != bson.TypeArray {
		return nil, ErrMalformedEvent
	}
	values, err := inviteesValue.Array().Values()
	if err != nil {
		return nil, ErrMalformedEvent
	}
	members := make([]MemberIdentity, 0, len(values))
	for _, value := range values {
		if value.Type != bson.TypeEmbeddedDocument {
			return nil, ErrMalformedEvent
		}
		member := value.Document()
		userID, err := exactInt64(member, "userId")
		if err != nil {
			return nil, ErrMalformedEvent
		}
		userTypeValue, err := member.LookupErr("userType")
		if err != nil || userTypeValue.Type != bson.TypeInt32 {
			return nil, ErrMalformedEvent
		}
		members = append(members, MemberIdentity{UserID: userID, UserType: userTypeValue.Int32()})
	}
	return MemberAdded{ChatID: chatID, LogID: logID, Members: members}, nil
}

func decodeChatStatusChanged(body []byte) (Event, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil {
		return nil, ErrMalformedEvent
	}
	chatID, err := exactInt64(raw, "chatId")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	plusUserID, err := exactInt64(raw, "plusUserId")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	revision, err := exactInt64(raw, "revision")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	statusValue, err := raw.LookupErr("chatStatus")
	if err != nil || statusValue.Type != bson.TypeEmbeddedDocument {
		return nil, ErrMalformedEvent
	}
	statusCopy := append(bson.Raw(nil), statusValue.Document()...)
	return ChatStatusChanged{ChatID: chatID, PlusUserID: plusUserID, Revision: revision, Status: statusCopy}, nil
}

func decodeChatMetaChanged(body []byte) (Event, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil {
		return nil, ErrMalformedEvent
	}
	chatID, err := exactInt64(raw, "chatId")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	metaValue, err := raw.LookupErr("meta")
	if err != nil || metaValue.Type != bson.TypeEmbeddedDocument {
		return nil, ErrMalformedEvent
	}
	meta := metaValue.Document()
	typeValue, err := meta.LookupErr("type")
	if err != nil || typeValue.Type != bson.TypeInt32 {
		return nil, ErrMalformedEvent
	}
	result := ChatMetaChanged{ChatID: chatID, Type: typeValue.Int32()}
	if result.Revision, err = optionalExactInt64(meta, "revision"); err != nil {
		return nil, ErrMalformedEvent
	}
	if result.AuthorID, err = optionalExactInt64(meta, "authorId"); err != nil {
		return nil, ErrMalformedEvent
	}
	if result.UpdatedAt, err = optionalExactInt64(meta, "updatedAt"); err != nil {
		return nil, ErrMalformedEvent
	}
	if content, present, err := optionalString(meta, "content"); err != nil {
		return nil, ErrMalformedEvent
	} else if present {
		result.Content = content
	}
	return result, nil
}

func decodeChatMCMetaChanged(body []byte) (Event, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil {
		return nil, ErrMalformedEvent
	}
	chatID, err := exactInt64(raw, "chatId")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	revisionValue, err := raw.LookupErr("revision")
	if err != nil || revisionValue.Type != bson.TypeInt32 {
		return nil, ErrMalformedEvent
	}
	typeValue, err := requiredString(raw, "type")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	content, err := requiredString(raw, "content")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	imageURL, _, err := optionalString(raw, "imageUrl")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	fullImageURL, _, err := optionalString(raw, "fullImageUrl")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	return ChatMCMetaChanged{
		ChatID:       chatID,
		Revision:     revisionValue.Int32(),
		Type:         typeValue,
		Content:      content,
		ImageURL:     imageURL,
		FullImageURL: fullImageURL,
	}, nil
}

func decodeChatLeft(body []byte) (Event, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil {
		return nil, ErrMalformedEvent
	}
	chatID, err := exactInt64(raw, "chatId")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	lastTokenID, err := exactInt64(raw, "lastTokenId")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	return ChatLeft{ChatID: chatID, LastTokenID: lastTokenID}, nil
}

func decodeReadState(body []byte) (Event, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil {
		return nil, ErrMalformedEvent
	}
	chatID, err := requiredInt64(raw, "chatId")
	if err != nil || chatID <= 0 {
		return nil, ErrMalformedEvent
	}
	userID, err := requiredInt64(raw, "userId")
	if err != nil || userID <= 0 {
		return nil, ErrMalformedEvent
	}
	watermark, err := requiredInt64(raw, "watermark")
	if err != nil || watermark <= 0 {
		return nil, ErrMalformedEvent
	}
	return ReadStateChanged{ChatID: chatID, UserID: userID, Watermark: watermark}, nil
}

func decodeMessage(packet loco.Packet) (Event, error) {
	raw := bson.Raw(packet.Body)
	chatID, logID, messageType, chatLog, err := messageEnvelope(raw)
	if err != nil {
		return nil, ErrMalformedEvent
	}
	switch messageType {
	case messagetype.Post:
		return decodePost(chatID, logID, chatLog)
	case messagetype.Vote:
		return decodeVote(chatID, logID, chatLog)
	case messagetype.Location:
		return decodeLocation(chatID, logID, chatLog)
	case messagetype.Profile:
		return decodeProfile(chatID, logID, chatLog)
	case messagetype.Text:
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
	case messagetype.Contact:
		contact, err := media.DecodeContactMessage(packet.Body)
		if err != nil {
			return nil, ErrMalformedEvent
		}
		return ContactMessage{Message: contact}, nil
	case messagetype.File:
		file, err := media.DecodeFileMessage(packet.Body)
		if err != nil {
			return nil, ErrMalformedEvent
		}
		return FileMessage{Message: file}, nil
	case messagetype.Audio:
		audio, err := media.DecodeAudioMessage(packet.Body)
		if err != nil {
			return nil, ErrMalformedEvent
		}
		return AudioMessage{Message: audio}, nil
	case messagetype.Video:
		video, err := media.DecodeVideoMessage(packet.Body)
		if err != nil {
			return nil, ErrMalformedEvent
		}
		return VideoMessage{Message: video}, nil
	case messagetype.MultiPhoto:
		album, err := media.DecodeMultiPhotoMessage(packet.Body)
		if err != nil {
			return nil, ErrMalformedEvent
		}
		return MultiPhotoMessage{Message: album}, nil
	case messagetype.Sticker, messagetype.AnimatedSticker:
		value, err := requiredString(chatLog, "attachment")
		if err != nil {
			return nil, ErrMalformedEvent
		}
		attachment, err := media.DecodeStickerAttachment(value)
		if err != nil {
			return nil, ErrMalformedEvent
		}
		return StickerMessage{ChatID: chatID, LogID: logID, AuthorID: optionalInt64(chatLog, "authorId"), SentAt: optionalInt64(chatLog, "sendAt"), Type: messageType, Attachment: attachment}, nil
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
	if metaType != 1 && metaType != 2 {
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
	if metaType == 1 {
		// Legacy pushes carry selection-keyed counts, not the type-2 rx
		// aggregate item IDs. Actors still come from the members lookup;
		// extra.my/userId describes one change, not the complete roster.
		var counts map[string]*int64
		if err := decodeSingleJSON(content, &counts); err != nil || counts == nil {
			return nil, ErrMalformedEvent
		}
		keys := make([]string, 0, len(counts))
		for key, count := range counts {
			if len(key) != 1 || key[0] < '1' || key[0] > '6' || count == nil || *count < 0 {
				return nil, ErrMalformedEvent
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		items := make([]ReactionItem, 0, len(keys))
		for _, key := range keys {
			items = append(items, ReactionItem{ID: key, Kind: 1, Count: *counts[key]})
		}
		return ReactionChanged{MetadataType: 1, ChatID: chatID, LinkID: linkID, LogID: logID, Revision: revision, Items: items}, nil
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
	return ReactionChanged{MetadataType: 2, ChatID: chatID, LinkID: linkID, LogID: logID, Revision: revision, Items: items}, nil
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

func exactInt64(raw bson.Raw, key string) (int64, error) {
	value, err := raw.LookupErr(key)
	if err != nil || value.Type != bson.TypeInt64 {
		return 0, ErrMalformedEvent
	}
	return value.Int64(), nil
}

func optionalExactInt64(raw bson.Raw, key string) (int64, error) {
	value, err := raw.LookupErr(key)
	if err != nil {
		return 0, nil
	}
	if value.Type != bson.TypeInt64 {
		return 0, ErrMalformedEvent
	}
	return value.Int64(), nil
}

func optionalString(raw bson.Raw, key string) (string, bool, error) {
	value, err := raw.LookupErr(key)
	if err != nil {
		return "", false, nil
	}
	if value.Type != bson.TypeString {
		return "", true, ErrMalformedEvent
	}
	return value.StringValue(), true, nil
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
