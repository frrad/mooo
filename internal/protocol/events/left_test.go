package events

import (
	"testing"

	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDecodeLeftCarriesChatAndCursorIdentity(t *testing.T) {
	body, err := bson.Marshal(bson.D{
		{Key: "chatId", Value: int64(42)},
		{Key: "lastTokenId", Value: int64(9001)},
	})
	if err != nil {
		t.Fatal(err)
	}

	event, err := Decode(loco.Packet{Header: loco.Header{Method: "LEFT"}, Body: body})
	if err != nil {
		t.Fatalf("Decode(LEFT) error = %v", err)
	}
	left, ok := event.(ChatLeft)
	if !ok {
		t.Fatalf("Decode(LEFT) event = %T, want ChatLeft", event)
	}
	if left.ChatID != 42 || left.LastTokenID != 9001 {
		t.Fatalf("LEFT = %#v, want chat 42/last token 9001", left)
	}
}
