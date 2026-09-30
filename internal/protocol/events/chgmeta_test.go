package events

import (
	"errors"
	"testing"

	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDecodeChangeMetaPreservesOpaqueSubtypeAndMetadataFields(t *testing.T) {
	body, err := bson.Marshal(bson.D{
		{Key: "chatId", Value: int64(42)},
		{Key: "meta", Value: bson.D{
			{Key: "type", Value: int32(14)},
			{Key: "revision", Value: int64(9)},
			{Key: "authorId", Value: int64(77)},
			{Key: "content", Value: "synthetic opaque content"},
			{Key: "updatedAt", Value: int64(1234)},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	event, err := Decode(loco.Packet{Header: loco.Header{Method: "CHGMETA"}, Body: body})
	if err != nil {
		t.Fatalf("Decode(CHGMETA) error = %v", err)
	}
	changed, ok := event.(ChatMetaChanged)
	if !ok {
		t.Fatalf("Decode(CHGMETA) event = %T, want ChatMetaChanged", event)
	}
	if changed.ChatID != 42 || changed.Type != 14 || changed.Revision != 9 ||
		changed.AuthorID != 77 || changed.Content != "synthetic opaque content" || changed.UpdatedAt != 1234 {
		t.Fatalf("CHGMETA = %#v, want chat 42/type 14/revision 9/author 77/content/timestamp", changed)
	}
}

func TestDecodeChangeMetaFailsClosedForUnprovenStructure(t *testing.T) {
	cases := []struct {
		name string
		body bson.D
	}{
		{name: "missing metadata", body: bson.D{{Key: "chatId", Value: int64(42)}}},
		{name: "wrong chat id width", body: bson.D{
			{Key: "chatId", Value: int32(42)},
			{Key: "meta", Value: bson.D{{Key: "type", Value: int32(14)}}},
		}},
		{name: "wrong metadata type width", body: bson.D{
			{Key: "chatId", Value: int64(42)},
			{Key: "meta", Value: bson.D{{Key: "type", Value: int64(14)}}},
		}},
		{name: "non-document metadata", body: bson.D{
			{Key: "chatId", Value: int64(42)},
			{Key: "meta", Value: "opaque"},
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			body, err := bson.Marshal(test.body)
			if err != nil {
				t.Fatal(err)
			}
			event, err := Decode(loco.Packet{Header: loco.Header{Method: "CHGMETA"}, Body: body})
			if event != nil || !errors.Is(err, ErrMalformedEvent) {
				t.Fatalf("event=%#v error=%v, want nil and ErrMalformedEvent", event, err)
			}
		})
	}
}
