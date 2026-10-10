package events

import (
	"encoding/json"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/messagetype"
)

const (
	KindMessageEdited  Kind = "message_edited"
	KindMessageDeleted Kind = "message_deleted"
	KindDeletedMessage Kind = "deleted_message"

	// Feed types carried in the JSON message of a type-0 chat log.
	feedTypeMessageDeleted = 14
	feedTypeMessageEdited  = 25
)

// MessageEdited is the feed log that records an edit of TargetLogID. It
// occupies its own position (LogID) in the chat's log sequence. A live
// SYNCMODMSG push also carries the edited message as Modified; the catch-up
// form carries only the target and its new revision.
type MessageEdited struct {
	ChatID         int64
	LogID          int64
	AuthorID       int64
	SentAt         int64
	TargetLogID    int64
	TargetRevision int64
	Modified       Event
}

func (MessageEdited) Kind() Kind { return KindMessageEdited }
func (MessageEdited) isEvent()   {}

func (m MessageEdited) String() string {
	return fmt.Sprintf("MessageEdited{chatId=%d, logId=%d, target=%d, revision=%d}", m.ChatID, m.LogID, m.TargetLogID, m.TargetRevision)
}

// MessageDeleted is the feed log that records a delete-for-everyone of
// TargetLogID by AuthorID. It occupies its own position in the log sequence.
type MessageDeleted struct {
	ChatID      int64
	LogID       int64
	AuthorID    int64
	SentAt      int64
	TargetLogID int64
	ByHost      bool
}

func (MessageDeleted) Kind() Kind { return KindMessageDeleted }
func (MessageDeleted) isEvent()   {}

func (m MessageDeleted) String() string {
	return fmt.Sprintf("MessageDeleted{chatId=%d, logId=%d, target=%d}", m.ChatID, m.LogID, m.TargetLogID)
}

// DeletedMessage is a chat log whose type carries the deleted-for-everyone
// flag. The server still returns the original content; it is never decoded.
type DeletedMessage struct {
	ChatID   int64
	LogID    int64
	AuthorID int64
	SentAt   int64
	BaseType int32
}

func (DeletedMessage) Kind() Kind { return KindDeletedMessage }
func (DeletedMessage) isEvent()   {}

func (m DeletedMessage) String() string {
	return fmt.Sprintf("DeletedMessage{chatId=%d, logId=%d, type=%d, content=<withheld>}", m.ChatID, m.LogID, m.BaseType)
}

type feedJSON struct {
	FeedType       json.Number `json:"feedType"`
	LogID          json.Number `json:"logId"`
	TargetRevision json.Number `json:"targetRevision"`
	ByHost         bool        `json:"byHost"`
}

// decodeFeed decodes a type-0 chat log. Only the edit and delete feeds are
// recognized here; any other feed is reported as malformed so delivery keeps
// an explicit gap for it.
func decodeFeed(chatID, logID int64, chatLog bson.Raw) (Event, error) {
	message, err := requiredString(chatLog, "message")
	if err != nil {
		return nil, ErrMalformedEvent
	}
	decoder := json.NewDecoder(strings.NewReader(message))
	decoder.UseNumber()
	var feed feedJSON
	if err := decoder.Decode(&feed); err != nil {
		return nil, ErrMalformedEvent
	}
	feedType, err := feed.FeedType.Int64()
	if err != nil {
		return nil, ErrMalformedEvent
	}
	target, err := feed.LogID.Int64()
	if err != nil || target <= 0 || target >= logID {
		return nil, ErrMalformedEvent
	}
	author, sentAt := optionalInt64(chatLog, "authorId"), optionalInt64(chatLog, "sendAt")
	switch feedType {
	case feedTypeMessageDeleted:
		return MessageDeleted{ChatID: chatID, LogID: logID, AuthorID: author, SentAt: sentAt, TargetLogID: target, ByHost: feed.ByHost}, nil
	case feedTypeMessageEdited:
		revision, err := feed.TargetRevision.Int64()
		if err != nil || revision <= 0 {
			return nil, ErrMalformedEvent
		}
		return MessageEdited{ChatID: chatID, LogID: logID, AuthorID: author, SentAt: sentAt, TargetLogID: target, TargetRevision: revision}, nil
	default:
		return nil, ErrMalformedEvent
	}
}

// decodeEditPush decodes SYNCMODMSG: the edit feed plus the edited message,
// which must be the feed's target at the feed's revision.
func decodeEditPush(packet loco.Packet) (Event, error) {
	event, err := decodeMessage(packet)
	if err != nil {
		return nil, err
	}
	edit, ok := event.(MessageEdited)
	if !ok {
		return nil, ErrMalformedEvent
	}
	raw := bson.Raw(packet.Body)
	value, err := raw.LookupErr("modifiedChatLog")
	if err != nil || value.Type != bson.TypeEmbeddedDocument {
		return nil, ErrMalformedEvent
	}
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: edit.ChatID}, {Key: "chatLog", Value: value.Document()}})
	if err != nil {
		return nil, ErrMalformedEvent
	}
	modified, err := decodeMessage(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
	if err != nil {
		return nil, ErrMalformedEvent
	}
	chatID, logID, ok := MessagePosition(modified)
	if !ok || chatID != edit.ChatID || logID != edit.TargetLogID || optionalInt64(value.Document(), "revision") != edit.TargetRevision {
		return nil, ErrMalformedEvent
	}
	edit.Modified = modified
	return edit, nil
}

// decodeDeletePush decodes SYNCDLMSG, whose chat log is the delete feed.
func decodeDeletePush(packet loco.Packet) (Event, error) {
	event, err := decodeMessage(packet)
	if err != nil {
		return nil, err
	}
	if _, ok := event.(MessageDeleted); !ok {
		return nil, ErrMalformedEvent
	}
	return event, nil
}

// deletedFlagged accepts only an ordinary base type with the deleted flag;
// values with other high bits stay unsupported.
func deletedFlagged(messageType int32) bool {
	base := messageType &^ messagetype.DeletedAllChatTypeFlag
	return messageType&messagetype.DeletedAllChatTypeFlag != 0 && base > 0 && base < messagetype.DeletedAllChatTypeFlag
}
