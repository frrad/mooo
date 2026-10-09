package events

import (
	"encoding/json"
	"io"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// VoteMessage is an inbound text-poll creation snapshot, not interactive voting.
type VoteMessage struct {
	ChatID, LogID, AuthorID, SentAt int64
	Title                           string
	Options                         []string
}

func (VoteMessage) Kind() Kind         { return KindVoteMessage }
func (VoteMessage) isEvent()           {}
func (VoteMessage) String() string     { return "VoteMessage{<redacted>}" }
func (m VoteMessage) GoString() string { return m.String() }

func decodeVote(chatID, logID int64, log bson.Raw) (Event, error) {
	value, err := requiredString(log, "attachment")
	if err != nil || len(value) > 64<<10 || !validEventString(value) {
		return nil, ErrMalformedEvent
	}
	dec := json.NewDecoder(strings.NewReader(value))
	dec.UseNumber()
	budget := 1024
	if err = validateBoundedJSON(dec, 0, &budget); err != nil {
		return nil, err
	}
	if _, err = dec.Token(); err != io.EOF {
		return nil, ErrMalformedEvent
	}
	var a struct {
		VoteID  *int32            `json:"voteId"`
		PostID  *string           `json:"postId"`
		Subtype int               `json:"subtype"`
		Title   string            `json:"title"`
		Objects []json.RawMessage `json:"os"`
	}
	// Modern observed cards use zero as the legacy vote-ID placeholder.
	if json.Unmarshal([]byte(value), &a) != nil || a.VoteID == nil || *a.VoteID < 0 || a.PostID != nil || a.Subtype != 1 || !validVoteText(a.Title, 1024) || len(a.Objects) == 0 || len(a.Objects) > 16 {
		return nil, ErrMalformedEvent
	}
	m := VoteMessage{ChatID: chatID, LogID: logID, AuthorID: optionalInt64(log, "authorId"), SentAt: optionalInt64(log, "sendAt"), Title: a.Title}
	found := false
	for _, raw := range a.Objects {
		var object struct {
			Type           int               `json:"t"`
			Subtype        int               `json:"st"`
			Title          string            `json:"tt"`
			ItemType       string            `json:"ittpe"`
			Items          []json.RawMessage `json:"its"`
			Unknown        bool              `json:"isUnknown"`
			ContentMessage string            `json:"contentMessage"`
		}
		if json.Unmarshal(raw, &object) != nil {
			return nil, ErrMalformedEvent
		}
		switch object.Type {
		case 2:
			// The observed navigation button is omitted; never act on its URL.
			if object.Subtype != 4 {
				return nil, ErrMalformedEvent
			}
		case 9:
			if found || object.Subtype != 1 || object.Title != a.Title || (object.ItemType != "" && object.ItemType != "text") || object.Unknown || object.ContentMessage != "" || len(object.Items) == 0 || len(object.Items) > 64 {
				return nil, ErrMalformedEvent
			}
			found = true
			for _, rawItem := range object.Items {
				var fields map[string]json.RawMessage
				if json.Unmarshal(rawItem, &fields) != nil || fields == nil {
					return nil, ErrMalformedEvent
				}
				for k := range fields {
					if k != "tt" && k != "th" {
						return nil, ErrMalformedEvent
					}
				}
				var item struct {
					Title     string `json:"tt"`
					Thumbnail string `json:"th"`
				}
				if json.Unmarshal(rawItem, &item) != nil || !validVoteText(item.Title, 4096) || item.Thumbnail != "" {
					return nil, ErrMalformedEvent
				}
				m.Options = append(m.Options, item.Title)
			}
		default:
			return nil, ErrMalformedEvent
		}
	}
	if !found {
		return nil, ErrMalformedEvent
	}
	return m, nil
}

func validVoteText(value string, max int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= max && validEventString(value)
}
