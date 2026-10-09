package events

import (
	"encoding/json"
	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
	"os"
	"testing"
)

func TestObservedBoardsAnnouncementPush(t *testing.T) {
	b, err := os.ReadFile("../../../research/fixtures/chatmeta/observed-moim-announcement.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Method string
		Input  json.RawMessage
	}
	if err = json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	var d bson.D
	if err = bson.UnmarshalExtJSON(f.Input, false, &d); err != nil {
		t.Fatal(err)
	}
	b, err = bson.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	e, err := Decode(loco.Packet{Header: loco.Header{Method: f.Method}, Body: b})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := e.(ChatMoimMetaChanged)
	if !ok || m.ChatID != 3000 || len(m.Metas) != 1 {
		t.Fatalf("event=%+v", e)
	}
	a, err := m.Metas[0].Announcement()
	if err != nil || !a.Active || a.Text != "Synthetic announcement" {
		t.Fatalf("announcement=%+v err=%v", a, err)
	}
}
