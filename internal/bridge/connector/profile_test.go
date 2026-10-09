package connector

import (
	"context"
	"encoding/json"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/event"
	"os"
	"strings"
	"testing"
)

func TestObservedProfileShapeConvertsWithoutPermit(t *testing.T) {
	b, err := os.ReadFile("../../../research/fixtures/profile/observed-shape.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Attachment json.RawMessage `json:"attachment"`
	}
	if json.Unmarshal(b, &f) != nil {
		t.Fatal("fixture invalid")
	}
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "type", Value: messagetype.Profile}, {Key: "attachment", Value: string(f.Attachment)}}}})
	if err != nil {
		t.Fatal(err)
	}
	e, err := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := e.(events.ProfileMessage)
	if !ok {
		t.Fatalf("profile untyped: %T", e)
	}
	chat, log, ok := events.MessagePosition(m)
	if !ok || chat != 42 || log != 99 {
		t.Fatal("identity lost")
	}
	got, err := convertProfile(context.Background(), nil, nil, m)
	if err != nil {
		t.Fatal(err)
	}
	c := got.Parts[0].Content
	if c.MsgType != event.MsgText || c.Body != "KakaoTalk profile: Synthetic Profile\nKakaoTalk user ID: 42" || strings.Contains(c.Body, "00000000") {
		t.Fatal("profile content/permit boundary lost")
	}
	if got.Parts[0].DBMetadata.(*KakaoMessageMetadata).Type != messagetype.Profile {
		t.Fatal("type lost")
	}
}
