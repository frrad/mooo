package connector

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/event"
)

func locationPacket(t *testing.T, attachment string) loco.Packet {
	t.Helper()
	b, err := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "authorId", Value: testOtherID}, {Key: "type", Value: messagetype.Location}, {Key: "attachment", Value: attachment}}}})
	if err != nil {
		t.Fatal(err)
	}
	return loco.Packet{Header: loco.Header{Method: "MSG"}, Body: b}
}
func TestObservedLocationNativeCoordinatesAndIdentity(t *testing.T) {
	b, err := os.ReadFile("../../../research/fixtures/location/observed-shape.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Attachment json.RawMessage `json:"attachment"`
	}
	if json.Unmarshal(b, &fixture) != nil {
		t.Fatal("fixture invalid")
	}
	e, err := events.DecodeForDelivery(locationPacket(t, string(fixture.Attachment)))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := e.(events.LocationMessage)
	if !ok {
		t.Fatalf("location untyped: %T", e)
	}
	for _, positioned := range []events.Event{m, &m} {
		chat, log, ok := events.MessagePosition(positioned)
		if !ok || chat != testChatID || log != 99 {
			t.Fatal("location identity lost")
		}
	}
	got, err := convertLocation(context.Background(), nil, nil, m)
	if err != nil || len(got.Parts) != 1 {
		t.Fatal("conversion failed", err)
	}
	c := got.Parts[0].Content
	if c.MsgType != event.MsgLocation || c.Body != "New York West 34th Street 20" || c.GeoURI != "geo:40.74839971667198,-73.98569975048304" || got.Parts[0].DBMetadata.(*KakaoMessageMetadata).Type != messagetype.Location {
		t.Fatal("native location lost observed fields")
	}
}
func TestLocationRejectsMissingOutOfRangeOrDuplicateCoordinates(t *testing.T) {
	for _, a := range []string{`{"lng":0}`, `{"lat":null,"lng":0}`, `{"lat":91,"lng":0}`, `{"lat":0,"lng":181}`, `{"lat":0,"lng":0,"lat":1}`, `{"lat":1e400,"lng":0}`, `{"lat":"1","lng":0}`} {
		if _, err := events.Decode(locationPacket(t, a)); err == nil {
			t.Fatal("malformed location accepted")
		}
		e, err := events.DecodeForDelivery(locationPacket(t, a))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := e.(events.MessageGap); !ok {
			t.Fatal("malformed location lost explicit delivery gap")
		}
	}
}
