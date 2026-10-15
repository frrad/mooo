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

	"github.com/frrad/mooo/internal/protocol/bsonfield"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type Kind string

const (
	KindTextMessage         Kind = "text_message"
	KindPostMessage         Kind = "post_message"
	KindVoteMessage         Kind = "vote_message"
	KindLocationMessage     Kind = "location_message"
	KindProfileMessage      Kind = "profile_message"
	KindReplyMessage        Kind = "reply_message"
	KindPhotoMessage        Kind = "photo_message"
	KindVideoMessage        Kind = "video_message"
	KindAudioMessage        Kind = "audio_message"
	KindFileMessage         Kind = "file_message"
	KindContactMessage      Kind = "contact_message"
	KindMultiPhotoMessage   Kind = "multi_photo_message"
	KindStickerMessage      Kind = "sticker_message"
	KindMessageGap          Kind = "message_gap"
	KindUnsupportedMessage  Kind = "unsupported_message"
	KindReactionChanged     Kind = "reaction_changed"
	KindReadStateChanged    Kind = "read_state_changed"
	KindChangeServer        Kind = "change_server"
	KindKickout             Kind = "kickout"
	KindMemberRemoved       Kind = "member_removed"
	KindMemberAdded         Kind = "member_added"
	KindChatLeft            Kind = "chat_left"
	KindChatStatusChanged   Kind = "chat_status_changed"
	KindChatMetaChanged     Kind = "chat_meta_changed"
	KindChatMoimMetaChanged Kind = "chat_moim_meta_changed"
	KindChatMCMetaChanged   Kind = "chat_mcmeta_changed"
	KindUnsupportedLogMeta  Kind = "unsupported_log_meta"
	KindUnknownPacket       Kind = "unknown_packet"
)

var ErrMalformedEvent = errors.New("events: malformed event")

// fields reads event fields. A present null is a type error.
var fields = bsonfield.Reader{Err: ErrMalformedEvent}

// ErrUnidentifiableMembership requires delivery to stop: a rejected notice may
// have revoked access, so later content cannot use the previous roster.
var ErrUnidentifiableMembership = fmt.Errorf("%w: membership identity unavailable", ErrMalformedEvent)

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
// Message, URL and URLLabel are the optional errMsg, errUrl and errUrlLabel
// strings that the Mac client shows in its logout alert; each is empty when
// absent or not a string.
type Kickout struct {
	Reason   int32
	Message  string
	URL      string
	URLLabel string
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

func (MemberRemoved) Kind() Kind           { return KindMemberRemoved }
func (m MemberRemoved) Position() Position { return Position{m.ChatID, m.LogID} }
func (MemberRemoved) isEvent()             {}

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

func (MemberAdded) Kind() Kind           { return KindMemberAdded }
func (m MemberAdded) Position() Position { return Position{m.ChatID, m.LogID} }
func (MemberAdded) isEvent()             {}

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

// ChatMoimMetaChanged is a Boards metadata refresh signal. It is separate
// from CHGMETA shared room metadata.
type ChatMoimMetaChanged struct {
	ChatID int64
	Metas  []chatmeta.MoimMeta
}

func (ChatMoimMetaChanged) Kind() Kind { return KindChatMoimMetaChanged }
func (ChatMoimMetaChanged) isEvent()   {}

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
	case "CHGMOMETAS":
		r, err := chatmeta.DecodeMoimResponse(packet.Body)
		if err != nil {
			return nil, ErrMalformedEvent
		}
		return ChatMoimMetaChanged{ChatID: r.ChatID, Metas: r.Metas}, nil
	case "CHGMETA":
		return decodeChatMetaChanged(packet.Body)
	case "CHGMCMETA":
		return decodeChatMCMetaChanged(packet.Body)
	case "LEFT":
		return decodeChatLeft(packet.Body)
	case "SYNCMODMSG":
		return decodeEditPush(packet)
	case "SYNCDLMSG":
		return decodeDeletePush(packet)
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
	return Kickout{
		Reason:   reason,
		Message:  optionalString(raw, "errMsg"),
		URL:      optionalString(raw, "errUrl"),
		URLLabel: optionalString(raw, "errUrlLabel"),
	}, nil
}

// optionalString returns a top-level string value, or "" when the key is
// absent or holds another type.
func optionalString(raw bson.Raw, key string) string {
	value, err := raw.LookupErr(key)
	if err != nil || value.Type != bson.TypeString {
		return ""
	}
	return value.StringValue()
}

func decodeMemberRemoved(body []byte) (Event, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil || hasAnyDuplicateExcept(raw) {
		return nil, ErrMalformedEvent
	}
	chatLogValue, err := raw.LookupErr("chatLog")
	if err != nil || chatLogValue.Type != bson.TypeEmbeddedDocument {
		return nil, ErrMalformedEvent
	}
	chatLog := chatLogValue.Document()
	chatID, ok, err := fields.Int64Exact(chatLog, "chatId")
	if err != nil || !ok || chatID <= 0 {
		return nil, ErrMalformedEvent
	}
	logID, ok, err := fields.Int64Exact(chatLog, "logId")
	if err != nil || !ok || logID <= 0 {
		return nil, ErrMalformedEvent
	}
	members, err := membershipMessageMembers(chatLog, false)
	if err != nil {
		return nil, err
	}
	member := members[0]
	return MemberRemoved{ChatID: chatID, LogID: logID, UserID: member.UserID, UserType: member.UserType}, nil
}

func decodeMemberAdded(body []byte) (Event, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil || hasAnyDuplicateExcept(raw) {
		return nil, ErrMalformedEvent
	}
	chatLogValue, err := raw.LookupErr("chatLog")
	if err != nil || chatLogValue.Type != bson.TypeEmbeddedDocument {
		return nil, ErrMalformedEvent
	}
	chatLog := chatLogValue.Document()
	chatID, ok, err := fields.Int64Exact(chatLog, "chatId")
	if err != nil || !ok || chatID <= 0 {
		return nil, ErrMalformedEvent
	}
	logID, ok, err := fields.Int64Exact(chatLog, "logId")
	if err != nil || !ok || logID <= 0 {
		return nil, ErrMalformedEvent
	}
	members, err := membershipMessageMembers(chatLog, true)
	if err != nil {
		return nil, err
	}
	return MemberAdded{ChatID: chatID, LogID: logID, Members: members}, nil
}

func decodeChatStatusChanged(body []byte) (Event, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil {
		return nil, ErrMalformedEvent
	}
	chatID, ok, err := fields.Int64Exact(raw, "chatId")
	if err != nil || !ok {
		return nil, ErrMalformedEvent
	}
	plusUserID, ok, err := fields.Int64Exact(raw, "plusUserId")
	if err != nil || !ok {
		return nil, ErrMalformedEvent
	}
	revision, ok, err := fields.Int64Exact(raw, "revision")
	if err != nil || !ok {
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
	chatID, ok, err := fields.Int64Exact(raw, "chatId")
	if err != nil || !ok {
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
	if result.Revision, _, err = fields.Int64Exact(meta, "revision"); err != nil {
		return nil, ErrMalformedEvent
	}
	if result.AuthorID, _, err = fields.Int64Exact(meta, "authorId"); err != nil {
		return nil, ErrMalformedEvent
	}
	if result.UpdatedAt, _, err = fields.Int64Exact(meta, "updatedAt"); err != nil {
		return nil, ErrMalformedEvent
	}
	if content, present, err := fields.String(meta, "content"); err != nil {
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
	chatID, ok, err := fields.Int64Exact(raw, "chatId")
	if err != nil || !ok {
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
	imageURL, _, err := fields.String(raw, "imageUrl")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	fullImageURL, _, err := fields.String(raw, "fullImageUrl")
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
	chatID, ok, err := fields.Int64Exact(raw, "chatId")
	if err != nil || !ok {
		return nil, ErrMalformedEvent
	}
	lastTokenID, ok, err := fields.Int64Exact(raw, "lastTokenId")
	if err != nil || !ok {
		return nil, ErrMalformedEvent
	}
	return ChatLeft{ChatID: chatID, LastTokenID: lastTokenID}, nil
}

func decodeReadState(body []byte) (Event, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil {
		return nil, ErrMalformedEvent
	}
	chatID, ok, err := fields.Int64(raw, "chatId")
	if err != nil || !ok || chatID <= 0 {
		return nil, ErrMalformedEvent
	}
	userID, ok, err := fields.Int64(raw, "userId")
	if err != nil || !ok || userID <= 0 {
		return nil, ErrMalformedEvent
	}
	watermark, ok, err := fields.Int64(raw, "watermark")
	if err != nil || !ok || watermark <= 0 {
		return nil, ErrMalformedEvent
	}
	return ReadStateChanged{ChatID: chatID, UserID: userID, Watermark: watermark}, nil
}

func decodeLogMeta(body []byte) (Event, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil {
		return nil, ErrMalformedEvent
	}
	chatID, ok, err := fields.Int64(raw, "chatId")
	if err != nil || !ok || chatID <= 0 {
		return nil, ErrMalformedEvent
	}
	logID, ok, err := fields.Int64(raw, "logId")
	if err != nil || !ok || logID <= 0 {
		return nil, ErrMalformedEvent
	}
	metaType, ok, err := fields.Int64(raw, "type")
	if err != nil || !ok || metaType <= 0 || metaType > int64(^uint32(0)>>1) {
		return nil, ErrMalformedEvent
	}
	if metaType != 1 && metaType != 2 {
		return UnsupportedLogMeta{ChatID: chatID, LogID: logID, Type: int32(metaType)}, nil
	}
	revision, ok, err := fields.Int64(raw, "revision")
	if err != nil || !ok || revision <= 0 {
		return nil, ErrMalformedEvent
	}
	linkID := bsonfield.Int64OrZero(raw, "linkId")
	if linkID < 0 {
		return nil, ErrMalformedEvent
	}
	content, err := requiredString(raw, "content")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	return logMetaEvent(chatID, logID, revision, linkID, metaType, content)
}

// DecodeSyncedLogMeta decodes one meta object from the HTTP resync endpoint.
// It carries the same fields and content as a CHGLOGMETA push, as JSON.
func DecodeSyncedLogMeta(item []byte) (Event, error) {
	var wire struct {
		ChatID   json.Number `json:"chatId"`
		LogID    json.Number `json:"logId"`
		Type     json.Number `json:"type"`
		Revision json.Number `json:"revision"`
		LinkID   json.Number `json:"linkId"`
		Content  *string     `json:"content"`
	}
	decoder := json.NewDecoder(bytes.NewReader(item))
	decoder.UseNumber()
	if err := decoder.Decode(&wire); err != nil || wire.Content == nil {
		return nil, ErrMalformedEvent
	}
	number := func(n json.Number, optional bool) (int64, bool) {
		if n == "" {
			return 0, optional
		}
		v, err := n.Int64()
		return v, err == nil
	}
	chatID, ok1 := number(wire.ChatID, false)
	logID, ok2 := number(wire.LogID, false)
	metaType, ok3 := number(wire.Type, false)
	linkID, ok4 := number(wire.LinkID, true)
	if !ok1 || !ok2 || !ok3 || !ok4 || chatID <= 0 || logID <= 0 || metaType <= 0 || metaType > int64(^uint32(0)>>1) || linkID < 0 {
		return nil, ErrMalformedEvent
	}
	if metaType != 1 && metaType != 2 {
		return UnsupportedLogMeta{ChatID: chatID, LogID: logID, Type: int32(metaType)}, nil
	}
	revision, ok := number(wire.Revision, false)
	if !ok || revision <= 0 {
		return nil, ErrMalformedEvent
	}
	return logMetaEvent(chatID, logID, revision, linkID, metaType, *wire.Content)
}

func logMetaEvent(chatID, logID, revision, linkID, metaType int64, content string) (Event, error) {
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

// requiredString reads a present string field that is valid UTF-8 without
// NUL characters.
func requiredString(raw bson.Raw, key string) (string, error) {
	value, ok, err := fields.String(raw, key)
	if err != nil || !ok || !validEventString(value) {
		return "", ErrMalformedEvent
	}
	return value, nil
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
