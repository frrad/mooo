package events

import (
	"encoding/json"
	"io"
	"math"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// LocationMessage preserves a received single location, not live updates.
type LocationMessage struct {
	ChatID, LogID, AuthorID, SentAt int64
	Latitude, Longitude             float64
	Address, Title                  string
	Current                         bool
}

func (LocationMessage) Kind() Kind         { return KindLocationMessage }
func (LocationMessage) isEvent()           {}
func (LocationMessage) String() string     { return "LocationMessage{<redacted>}" }
func (m LocationMessage) GoString() string { return m.String() }
func decodeLocation(chatID, logID int64, log bson.Raw) (Event, error) {
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
		Latitude  *float64 `json:"lat"`
		Longitude *float64 `json:"lng"`
		Address   string   `json:"a"`
		Title     string   `json:"t"`
		Current   bool     `json:"c"`
	}
	if json.Unmarshal([]byte(value), &a) != nil || a.Latitude == nil || a.Longitude == nil || math.IsNaN(*a.Latitude) || math.IsInf(*a.Latitude, 0) || *a.Latitude < -90 || *a.Latitude > 90 || math.IsNaN(*a.Longitude) || math.IsInf(*a.Longitude, 0) || *a.Longitude < -180 || *a.Longitude > 180 || len(a.Address) > 4096 || len(a.Title) > 1024 || !validEventString(a.Address) || !validEventString(a.Title) {
		return nil, ErrMalformedEvent
	}
	return LocationMessage{ChatID: chatID, LogID: logID, AuthorID: optionalInt64(log, "authorId"), SentAt: optionalInt64(log, "sendAt"), Latitude: *a.Latitude, Longitude: *a.Longitude, Address: a.Address, Title: a.Title, Current: a.Current}, nil
}
