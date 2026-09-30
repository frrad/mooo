package events

import (
	"errors"
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

func TestDecodeLeftFailsClosedForMissingOrWrongWidth(t *testing.T) {
	cases := []bson.D{
		{{Key: "chatId", Value: int64(42)}},
		{{Key: "lastTokenId", Value: int64(9001)}},
		{{Key: "chatId", Value: int32(42)}, {Key: "lastTokenId", Value: int64(9001)}},
		{{Key: "chatId", Value: int64(42)}, {Key: "lastTokenId", Value: int32(9001)}},
	}
	for index, document := range cases {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			body, err := bson.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			event, err := Decode(loco.Packet{Header: loco.Header{Method: "LEFT"}, Body: body})
			if event != nil || !errors.Is(err, ErrMalformedEvent) {
				t.Fatalf("event=%#v error=%v, want nil and ErrMalformedEvent", event, err)
			}
		})
	}
}
