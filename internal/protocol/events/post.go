package events

import (
	"encoding/json"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// PostMessage preserves a text-only board-post snapshot, not board interaction.
// PostMessage is a Boards post snapshot. Photos counts photo thumbnails,
// PollTitle names an attached poll, and Unrendered counts post objects the
// bridge does not summarize.
type PostMessage struct {
	ChatID, LogID, AuthorID, SentAt int64
	Text                            string
	Photos                          int
	PollTitle                       string
	Unrendered                      int
}

func (PostMessage) Kind() Kind         { return KindPostMessage }
func (PostMessage) isEvent()           {}
func (PostMessage) String() string     { return "PostMessage{<redacted>}" }
func (m PostMessage) GoString() string { return m.String() }

func decodePost(chatID, logID int64, log bson.Raw) (Event, error) {
	value, err := requiredString(log, "attachment")
	if err != nil || !validBoundedEventJSON(value) {
		return nil, ErrMalformedEvent
	}
	var a struct {
		Objects []json.RawMessage `json:"os"`
	}
	if json.Unmarshal([]byte(value), &a) != nil || len(a.Objects) == 0 || len(a.Objects) > 16 {
		return nil, ErrMalformedEvent
	}
	m := PostMessage{ChatID: chatID, LogID: logID, AuthorID: optionalInt64(log, "authorId"), SentAt: optionalInt64(log, "sendAt")}
	found := false
	for _, raw := range a.Objects {
		var object struct {
			Type       int               `json:"t"`
			Subtype    int               `json:"st"`
			Content    *string           `json:"ct"`
			Structured *string           `json:"jct"`
			Thumbnails []json.RawMessage `json:"th"`
			PollTitle  *string           `json:"tt"`
		}
		if json.Unmarshal(raw, &object) != nil {
			return nil, ErrMalformedEvent
		}
		switch object.Type {
		case 2:
			// Preserve the snapshot without following or forwarding its board
			// URL. Observed subtypes: 1 (post), 4 (poll post).
			if object.Subtype < 0 {
				return nil, ErrMalformedEvent
			}
		case 3:
			// Observed on announcement posts; it carries no rendered text.
		case 5:
			// Photos: one thumbnail entry per photo.
			if len(object.Thumbnails) == 0 || len(object.Thumbnails) > 64 {
				return nil, ErrMalformedEvent
			}
			m.Photos += len(object.Thumbnails)
		case 9:
			if object.PollTitle == nil || !validEventString(*object.PollTitle) || len(*object.PollTitle) > 4<<10 || m.PollTitle != "" {
				return nil, ErrMalformedEvent
			}
			m.PollTitle = *object.PollTitle
		case 1:
			if found || object.Structured == nil || !validBoundedEventJSON(*object.Structured) || (object.Content != nil && (!validEventString(*object.Content) || len(*object.Content) > 32<<10)) {
				return nil, ErrMalformedEvent
			}
			found = true
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) != nil {
				return nil, ErrMalformedEvent
			}
			for k := range fields {
				if k != "t" && k != "ct" && k != "jct" {
					return nil, ErrMalformedEvent
				}
			}
			var elements []json.RawMessage
			if json.Unmarshal([]byte(*object.Structured), &elements) != nil || len(elements) == 0 || len(elements) > 128 {
				return nil, ErrMalformedEvent
			}
			var text strings.Builder
			for _, element := range elements {
				var fields map[string]json.RawMessage
				if json.Unmarshal(element, &fields) != nil || fields == nil {
					return nil, ErrMalformedEvent
				}
				for k := range fields {
					if k != "type" && k != "text" {
						return nil, ErrMalformedEvent
					}
				}
				var part struct {
					Type string  `json:"type"`
					Text *string `json:"text"`
				}
				if json.Unmarshal(element, &part) != nil || part.Type != "text" || part.Text == nil || !validEventString(*part.Text) || len(*part.Text) > 16<<10 || text.Len()+len(*part.Text) > 32<<10 {
					return nil, ErrMalformedEvent
				}
				text.WriteString(*part.Text)
			}
			m.Text = text.String()
		default:
			// Media, schedules, quizzes and future forms are counted, not
			// dropped, so the post still reaches Matrix.
			m.Unrendered++
		}
	}
	if found && strings.TrimSpace(m.Text) == "" {
		return nil, ErrMalformedEvent
	}
	if !found && m.Photos == 0 && strings.TrimSpace(m.PollTitle) == "" && m.Unrendered == 0 {
		return nil, ErrMalformedEvent
	}
	return m, nil
}
