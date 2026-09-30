package events

import (
	"errors"
	"testing"

	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDecodeChangeChatStatusPreservesTypedIdentityAndRawStatus(t *testing.T) {
	body, err := bson.Marshal(bson.D{
		{Key: "chatId", Value: int64(42)},
		{Key: "plusUserId", Value: int64(77)},
		{Key: "revision", Value: int64(9)},
		{Key: "chatStatus", Value: bson.D{{Key: "synthetic", Value: "opaque"}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	event, err := Decode(loco.Packet{Header: loco.Header{Method: "CHGCHATST"}, Body: body})
	if err != nil {
		t.Fatalf("Decode(CHGCHATST) error = %v", err)
	}
	changed, ok := event.(ChatStatusChanged)
	if !ok {
		t.Fatalf("Decode(CHGCHATST) event = %T, want ChatStatusChanged", event)
	}
	if changed.ChatID != 42 || changed.PlusUserID != 77 || changed.Revision != 9 {
		t.Fatalf("CHGCHATST = %#v, want chat 42/plus user 77/revision 9", changed)
	}
	if len(changed.Status) == 0 || bson.Raw(changed.Status).Validate() != nil {
		t.Fatalf("CHGCHATST status = %#v, want a valid opaque BSON document", changed.Status)
	}
}

func TestDecodeChangeChatStatusFailsClosedForMissingOrWrongWidth(t *testing.T) {
	cases := []bson.D{
		{{Key: "plusUserId", Value: int64(77)}, {Key: "revision", Value: int64(9)}, {Key: "chatStatus", Value: bson.D{}}},
		{{Key: "chatId", Value: int64(42)}, {Key: "revision", Value: int64(9)}, {Key: "chatStatus", Value: bson.D{}}},
		{{Key: "chatId", Value: int64(42)}, {Key: "plusUserId", Value: int32(77)}, {Key: "revision", Value: int64(9)}, {Key: "chatStatus", Value: bson.D{}}},
		{{Key: "chatId", Value: int64(42)}, {Key: "plusUserId", Value: int64(77)}, {Key: "revision", Value: int32(9)}, {Key: "chatStatus", Value: bson.D{}}},
		{{Key: "chatId", Value: int64(42)}, {Key: "plusUserId", Value: int64(77)}, {Key: "revision", Value: int64(9)}, {Key: "chatStatus", Value: "not-a-document"}},
	}
	for index, document := range cases {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			body, err := bson.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			event, err := Decode(loco.Packet{Header: loco.Header{Method: "CHGCHATST"}, Body: body})
			if event != nil || !errors.Is(err, ErrMalformedEvent) {
				t.Fatalf("event=%#v error=%v, want nil and ErrMalformedEvent", event, err)
			}
		})
	}
}

func TestDecodeChangeChatStatusOwnsOpaqueStatusBytes(t *testing.T) {
	status := bson.D{{Key: "synthetic", Value: "opaque"}}
	body, err := bson.Marshal(bson.D{
		{Key: "chatId", Value: int64(42)},
		{Key: "plusUserId", Value: int64(77)},
		{Key: "revision", Value: int64(9)},
		{Key: "chatStatus", Value: status},
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := Decode(loco.Packet{Header: loco.Header{Method: "CHGCHATST"}, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	changed := event.(ChatStatusChanged)
	original := append([]byte(nil), changed.Status...)
	body[len(body)-1] ^= 0xff
	if string(changed.Status) != string(original) {
		t.Fatal("status bytes alias packet body")
	}
}
