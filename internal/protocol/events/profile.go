package events

import (
	"encoding/json"
	"io"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
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
	if err != nil || len(value) > 64<<10 || !validEventString(value) {
		return nil, ErrMalformedEvent
	}
	dec := json.NewDecoder(strings.NewReader(value))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, ErrMalformedEvent
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err = dec.Token()
		key, ok := tok.(string)
		if err != nil || !ok || seen[key] || len(seen) >= 64 {
			return nil, ErrMalformedEvent
		}
		seen[key] = true
		var v json.RawMessage
		if dec.Decode(&v) != nil {
			return nil, ErrMalformedEvent
		}
	}
	if _, err = dec.Token(); err != nil {
		return nil, ErrMalformedEvent
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return nil, ErrMalformedEvent
	}
	var a struct {
		UserID        int64  `json:"userId"`
		NickName      string `json:"nickName"`
		StatusMessage string `json:"statusMessage"`
	}
	if json.Unmarshal([]byte(value), &a) != nil || a.UserID <= 0 || strings.TrimSpace(a.NickName) == "" || len(a.NickName) > 1024 || len(a.StatusMessage) > 16<<10 || !validEventString(a.NickName) || !validEventString(a.StatusMessage) {
		return nil, ErrMalformedEvent
	}
	return ProfileMessage{ChatID: chatID, LogID: logID, AuthorID: optionalInt64(log, "authorId"), SentAt: optionalInt64(log, "sendAt"), UserID: a.UserID, NickName: a.NickName, StatusMessage: a.StatusMessage}, nil
}
