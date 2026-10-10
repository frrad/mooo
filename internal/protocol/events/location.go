package events

import (
	"math"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/bsonfield"
	"github.com/frrad/mooo/internal/protocol/strictjson"
)

// LocationMessage preserves a received single location, not live updates.
type LocationMessage struct {
	ChatID, LogID, AuthorID, SentAt int64
	Latitude, Longitude             float64
	Address, Title                  string
	Current                         bool
}

func (LocationMessage) Kind() Kind           { return KindLocationMessage }
func (m LocationMessage) Position() Position { return Position{m.ChatID, m.LogID} }
func (LocationMessage) isEvent()             {}
func (LocationMessage) String() string       { return "LocationMessage{<redacted>}" }
func (m LocationMessage) GoString() string   { return m.String() }
func decodeLocation(chatID, logID int64, log bson.Raw) (Event, error) {
	value, err := requiredString(log, "attachment")
	if err != nil || !validEventString(value) {
		return nil, ErrMalformedEvent
	}
	var a struct {
		Latitude  *float64 `json:"lat"`
		Longitude *float64 `json:"lng"`
		Address   string   `json:"a"`
		Title     string   `json:"t"`
		Current   bool     `json:"c"`
	}
	if strictjson.Unmarshal([]byte(value), &a, attachmentJSON) != nil || a.Latitude == nil || a.Longitude == nil || math.IsNaN(*a.Latitude) || math.IsInf(*a.Latitude, 0) || *a.Latitude < -90 || *a.Latitude > 90 || math.IsNaN(*a.Longitude) || math.IsInf(*a.Longitude, 0) || *a.Longitude < -180 || *a.Longitude > 180 || len(a.Address) > 4096 || len(a.Title) > 1024 || !validEventString(a.Address) || !validEventString(a.Title) {
		return nil, ErrMalformedEvent
	}
	return LocationMessage{ChatID: chatID, LogID: logID, AuthorID: bsonfield.Int64OrZero(log, "authorId"), SentAt: bsonfield.Int64OrZero(log, "sendAt"), Latitude: *a.Latitude, Longitude: *a.Longitude, Address: a.Address, Title: a.Title, Current: a.Current}, nil
}
