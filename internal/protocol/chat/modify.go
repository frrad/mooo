package chat

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const (
	ModifyCommand = "MODIFYMSG"
	DeleteCommand = "DELETEMSG"

	// Server statuses the Mac client special-cases for DELETEMSG.
	StatusDeleteTypeNotAllowed = -210
	StatusAlreadyDeleted       = -211
	StatusDeleteTimeExpired    = -212
)

// ModifyRequest edits one own message. Type is the message's original type;
// Extra replaces its attachment.
type ModifyRequest struct {
	ChatID  int64
	LogID   int64
	Type    int32
	Message string
	Extra   string
}

func (r ModifyRequest) MarshalBSON() ([]byte, error) {
	if r.ChatID <= 0 {
		return nil, ErrInvalidChatID
	}
	if r.LogID <= 0 {
		return nil, ErrInvalidMessageID
	}
	if r.Type <= 0 || r.Message == "" || !utf8.ValidString(r.Message) || len(r.Message) > maxTextBytes || strings.IndexByte(r.Message, 0) >= 0 || !json.Valid([]byte(r.Extra)) {
		return nil, ErrInvalidMessage
	}
	return bson.Marshal(bson.D{
		{Key: "chatId", Value: r.ChatID}, {Key: "logId", Value: r.LogID}, {Key: "type", Value: r.Type},
		{Key: "msg", Value: r.Message}, {Key: "extra", Value: r.Extra},
	})
}

// DecodeModifyResponse returns the edited message's new revision. The
// response carries the edit feed and the edited message, like SYNCMODMSG.
func DecodeModifyResponse(body []byte, logID int64) (int64, error) {
	value, err := bson.Raw(body).LookupErr("modifiedChatLog")
	if err != nil || value.Type != bson.TypeEmbeddedDocument {
		return 0, ErrInvalidWriteResp
	}
	modified := value.Document()
	got, err := int64Field(modified, "logId")
	if err != nil || got != logID {
		return 0, ErrInvalidWriteResp
	}
	revision, err := int64Field(modified, "revision")
	if err != nil || revision <= 0 {
		return 0, ErrInvalidWriteResp
	}
	return revision, nil
}

// DeleteRequest deletes one own message for everyone.
type DeleteRequest struct {
	ChatID int64
	LogID  int64
}

func (r DeleteRequest) MarshalBSON() ([]byte, error) {
	if r.ChatID <= 0 {
		return nil, ErrInvalidChatID
	}
	if r.LogID <= 0 {
		return nil, ErrInvalidMessageID
	}
	return bson.Marshal(bson.D{{Key: "chatId", Value: r.ChatID}, {Key: "logId", Value: r.LogID}})
}
