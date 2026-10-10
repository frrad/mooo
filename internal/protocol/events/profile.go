package events

import (
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/bsonfield"
	"github.com/frrad/mooo/internal/protocol/strictjson"
)

// ProfileMessage preserves the identity and text of an inbound shared profile.
// Access permits and avatar URLs are not forwarded or acted upon.
type ProfileMessage struct {
	ChatID, LogID, AuthorID, SentAt int64
	UserID                          int64
	NickName, StatusMessage         string
}

func (ProfileMessage) Kind() Kind         { return KindProfileMessage }
func (ProfileMessage) isEvent()           {}
func (ProfileMessage) String() string     { return "ProfileMessage{<redacted>}" }
func (m ProfileMessage) GoString() string { return m.String() }
func decodeProfile(chatID, logID int64, log bson.Raw) (Event, error) {
	value, err := requiredString(log, "attachment")
	if err != nil || !validEventString(value) {
		return nil, ErrMalformedEvent
	}
	var a struct {
		UserID        int64  `json:"userId"`
		NickName      string `json:"nickName"`
		StatusMessage string `json:"statusMessage"`
	}
	if strictjson.Unmarshal([]byte(value), &a, attachmentJSON) != nil || a.UserID <= 0 || strings.TrimSpace(a.NickName) == "" || len(a.NickName) > 1024 || len(a.StatusMessage) > 16<<10 || !validEventString(a.NickName) || !validEventString(a.StatusMessage) {
		return nil, ErrMalformedEvent
	}
	return ProfileMessage{ChatID: chatID, LogID: logID, AuthorID: bsonfield.Int64OrZero(log, "authorId"), SentAt: bsonfield.Int64OrZero(log, "sendAt"), UserID: a.UserID, NickName: a.NickName, StatusMessage: a.StatusMessage}, nil
}
