package events

import (
	"errors"
	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
	"testing"
)

func gapPacket(t *testing.T, outer bson.D, log bson.D) loco.Packet {
	t.Helper()
	outer = append(outer, bson.E{Key: "chatLog", Value: log})
	body, e := bson.Marshal(outer)
	if e != nil {
		t.Fatal(e)
	}
	return loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body}
}
func TestDecodeForDeliveryRetainsMalformedContentIdentity(t *testing.T) {
	for _, typ := range []int32{1, 2, chat.ReplyType} {
		p := gapPacket(t, bson.D{{Key: "chatId", Value: int64(42)}}, bson.D{{Key: "logId", Value: int64(100)}, {Key: "type", Value: typ}, {Key: "authorId", Value: int64(200)}, {Key: "sendAt", Value: int64(300)}, {Key: "attachment", Value: "{private malformed"}})
		if _, e := Decode(p); !errors.Is(e, ErrMalformedEvent) {
			t.Fatalf("strict parse=%v", e)
		}
		got, e := DecodeForDelivery(p)
		if e != nil {
			t.Fatal(e)
		}
		want := MessageGap{ChatID: 42, LogID: 100, AuthorID: 200, SentAt: 300, Type: typ}
		if got != want {
			t.Fatalf("event=%#v", got)
		}
		chatID, logID, ok := MessagePosition(got)
		if !ok || chatID != 42 || logID != 100 {
			t.Fatal("gap lost cursor")
		}
	}
}
func TestDecodeForDeliveryRejectsAmbiguousIdentity(t *testing.T) {
	base := bson.D{{Key: "logId", Value: int64(100)}, {Key: "type", Value: int32(2)}, {Key: "attachment", Value: "{}"}}
	cases := []struct{ outer, log bson.D }{
		{bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatId", Value: int64(43)}}, base},
		{bson.D{{Key: "chatId", Value: int64(42)}}, append(append(bson.D{}, base...), bson.E{Key: "logId", Value: int64(101)})},
		{bson.D{{Key: "chatId", Value: int64(42)}, {Key: "logId", Value: int64(101)}}, base},
		{bson.D{{Key: "chatId", Value: int64(42)}, {Key: "logId", Value: int64(100)}}, bson.D{{Key: "logId", Value: "bad"}, {Key: "type", Value: int32(2)}}},
		{bson.D{{Key: "chatId", Value: int64(0)}}, base},
	}
	for i, c := range cases {
		got, e := DecodeForDelivery(gapPacket(t, c.outer, c.log))
		if got != nil || !errors.Is(e, ErrMalformedEvent) {
			t.Fatalf("case%d=%#v/%v", i, got, e)
		}
	}
}
