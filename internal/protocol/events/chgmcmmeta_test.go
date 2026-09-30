package events

import (
	"errors"
	"testing"

	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDecodeChangeMCMetaPreservesTypedFields(t *testing.T) {
	body, err := bson.Marshal(bson.D{
		{Key: "chatId", Value: int64(42)},
		{Key: "revision", Value: int32(9)},
		{Key: "type", Value: "opaque-type"},
		{Key: "content", Value: "opaque-content"},
		{Key: "imageUrl", Value: "https://synthetic.invalid/image"},
		{Key: "fullImageUrl", Value: "https://synthetic.invalid/full-image"},
	})
	if err != nil {
		t.Fatal(err)
	}

	event, err := Decode(loco.Packet{Header: loco.Header{Method: "CHGMCMETA"}, Body: body})
	if err != nil {
		t.Fatalf("Decode(CHGMCMETA) error = %v", err)
	}
	changed, ok := event.(ChatMCMetaChanged)
	if !ok {
		t.Fatalf("Decode(CHGMCMETA) event = %T, want ChatMCMetaChanged", event)
	}
	if changed.ChatID != 42 || changed.Revision != 9 || changed.Type != "opaque-type" ||
		changed.Content != "opaque-content" || changed.ImageURL != "https://synthetic.invalid/image" ||
		changed.FullImageURL != "https://synthetic.invalid/full-image" {
		t.Fatalf("CHGMCMETA = %#v, want all six model fields preserved", changed)
	}
}

func TestDecodeChangeMCMetaAllowsAbsentImages(t *testing.T) {
	body, err := bson.Marshal(bson.D{
		{Key: "chatId", Value: int64(42)},
		{Key: "revision", Value: int32(9)},
		{Key: "type", Value: "opaque-type"},
		{Key: "content", Value: "opaque-content"},
	})
	if err != nil {
		t.Fatal(err)
	}
	event, err := Decode(loco.Packet{Header: loco.Header{Method: "CHGMCMETA"}, Body: body})
	if err != nil {
		t.Fatalf("Decode(CHGMCMETA) error = %v", err)
	}
	changed, ok := event.(ChatMCMetaChanged)
	if !ok {
		t.Fatalf("Decode(CHGMCMETA) event = %T, want ChatMCMetaChanged", event)
	}
	if changed.ImageURL != "" || changed.FullImageURL != "" {
		t.Fatalf("CHGMCMETA minimal = %#v, want empty optional image URLs", changed)
	}
}

func TestDecodeChangeMCMetaFailsClosedForUnprovenStructure(t *testing.T) {
	cases := []struct {
		name string
		body bson.D
	}{
		{name: "missing chat id", body: bson.D{
			{Key: "revision", Value: int32(9)},
			{Key: "type", Value: "opaque-type"},
			{Key: "content", Value: "opaque-content"},
		}},
		{name: "missing revision", body: bson.D{
			{Key: "chatId", Value: int64(42)},
			{Key: "type", Value: "opaque-type"},
			{Key: "content", Value: "opaque-content"},
		}},
		{name: "wrong chat id width", body: bson.D{
			{Key: "chatId", Value: int32(42)},
			{Key: "revision", Value: int32(9)},
			{Key: "type", Value: "opaque-type"},
			{Key: "content", Value: "opaque-content"},
		}},
		{name: "wrong revision width", body: bson.D{
			{Key: "chatId", Value: int64(42)},
			{Key: "revision", Value: int64(9)},
			{Key: "type", Value: "opaque-type"},
			{Key: "content", Value: "opaque-content"},
		}},
		{name: "wrong type encoding", body: bson.D{
			{Key: "chatId", Value: int64(42)},
			{Key: "revision", Value: int32(9)},
			{Key: "type", Value: int32(1)},
			{Key: "content", Value: "opaque-content"},
		}},
		{name: "wrong content encoding", body: bson.D{
			{Key: "chatId", Value: int64(42)},
			{Key: "revision", Value: int32(9)},
			{Key: "type", Value: "opaque-type"},
			{Key: "content", Value: int32(1)},
		}},
		{name: "wrong image URL encoding", body: bson.D{
			{Key: "chatId", Value: int64(42)},
			{Key: "revision", Value: int32(9)},
			{Key: "type", Value: "opaque-type"},
			{Key: "content", Value: "opaque-content"},
			{Key: "imageUrl", Value: int32(1)},
		}},
		{name: "wrong full image URL encoding", body: bson.D{
			{Key: "chatId", Value: int64(42)},
			{Key: "revision", Value: int32(9)},
			{Key: "type", Value: "opaque-type"},
			{Key: "content", Value: "opaque-content"},
			{Key: "fullImageUrl", Value: int32(1)},
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			body, err := bson.Marshal(test.body)
			if err != nil {
				t.Fatal(err)
			}
			event, err := Decode(loco.Packet{Header: loco.Header{Method: "CHGMCMETA"}, Body: body})
			if event != nil || !errors.Is(err, ErrMalformedEvent) {
				t.Fatalf("event=%#v error=%v, want nil and ErrMalformedEvent", event, err)
			}
		})
	}
}
