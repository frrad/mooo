package events

import (
	"errors"
	"testing"

	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDecodeDelMemCarriesChatAndDepartedMemberIdentity(t *testing.T) {
	body, err := bson.Marshal(bson.D{{Key: "chatLog", Value: bson.D{
		{Key: "chatId", Value: int64(42)}, {Key: "logId", Value: int64(101)},
		{Key: "type", Value: int32(0)}, {Key: "message", Value: `{"feedType":2,"member":{"userId":7,"userType":1}}`},
	}}})
	if err != nil {
		t.Fatal(err)
	}

	event, err := Decode(loco.Packet{Header: loco.Header{Method: "DELMEM"}, Body: body})
	if err != nil {
		t.Fatalf("Decode(DELMEM) error = %v", err)
	}
	removed, ok := event.(MemberRemoved)
	if !ok {
		t.Fatalf("Decode(DELMEM) event = %T, want MemberRemoved", event)
	}
	if removed.ChatID != 42 || removed.LogID != 101 || removed.UserID != 7 || removed.UserType != 1 {
		t.Fatalf("DELMEM = %#v, want chat 42/log 101/user 7/type 1", removed)
	}
}

func TestDecodeDelMemFailsClosedForUnprovenStructure(t *testing.T) {
	cases := []struct {
		name string
		body bson.D
	}{
		{name: "missing chat log", body: bson.D{}},
		{name: "wrong chat id width", body: bson.D{{Key: "chatLog", Value: bson.D{{Key: "chatId", Value: int32(42)}}}}},
		{name: "missing leaver", body: bson.D{{Key: "chatLog", Value: bson.D{
			{Key: "chatId", Value: int64(42)}, {Key: "logId", Value: int64(101)}, {Key: "feed", Value: bson.D{}},
		}}}},
		{name: "wrong user type width", body: bson.D{{Key: "chatLog", Value: bson.D{
			{Key: "chatId", Value: int64(42)}, {Key: "logId", Value: int64(101)},
			{Key: "feed", Value: bson.D{{Key: "leaver", Value: bson.D{
				{Key: "userId", Value: int64(7)}, {Key: "userType", Value: int64(1)},
			}}}},
		}}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			body, err := bson.Marshal(test.body)
			if err != nil {
				t.Fatal(err)
			}
			event, err := Decode(loco.Packet{Header: loco.Header{Method: "DELMEM"}, Body: body})
			if event != nil || !errors.Is(err, ErrMalformedEvent) {
				t.Fatalf("event=%#v error=%v, want nil and ErrMalformedEvent", event, err)
			}
		})
	}
}
