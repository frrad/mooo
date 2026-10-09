package events

import (
	"errors"
	"testing"

	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDecodeNewMemCarriesChatAndInviteeIdentity(t *testing.T) {
	body, err := bson.Marshal(bson.D{{Key: "chatLog", Value: bson.D{
		{Key: "chatId", Value: int64(42)}, {Key: "logId", Value: int64(102)},
		{Key: "type", Value: int32(0)}, {Key: "message", Value: `{"feedType":1,"members":[{"userId":7,"userType":1},{"userId":8,"userType":2}]}`},
	}}})
	if err != nil {
		t.Fatal(err)
	}

	event, err := Decode(loco.Packet{Header: loco.Header{Method: "NEWMEM"}, Body: body})
	if err != nil {
		t.Fatalf("Decode(NEWMEM) error = %v", err)
	}
	added, ok := event.(MemberAdded)
	if !ok {
		t.Fatalf("Decode(NEWMEM) event = %T, want MemberAdded", event)
	}
	if added.ChatID != 42 || added.LogID != 102 || len(added.Members) != 2 {
		t.Fatalf("NEWMEM = %#v, want chat 42/log 102 with two invitees", added)
	}
	if added.Members[0].UserID != 7 || added.Members[0].UserType != 1 ||
		added.Members[1].UserID != 8 || added.Members[1].UserType != 2 {
		t.Fatalf("NEWMEM members = %#v, want ids/types 7/1 and 8/2", added.Members)
	}
}

func TestDecodeNewMemFailsClosedForUnprovenStructure(t *testing.T) {
	validChat := bson.D{{Key: "chatId", Value: int64(42)}, {Key: "logId", Value: int64(102)}}
	cases := []struct {
		name string
		body bson.D
	}{
		{name: "missing chat log", body: bson.D{}},
		{name: "missing feed", body: bson.D{{Key: "chatLog", Value: validChat}}},
		{name: "missing invitees", body: bson.D{{Key: "chatLog", Value: append(validChat, bson.E{Key: "feed", Value: bson.D{}})}}},
		{name: "non-document invitee", body: bson.D{{Key: "chatLog", Value: append(validChat, bson.E{Key: "feed", Value: bson.D{{Key: "invitees", Value: bson.A{"bad"}}}})}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			body, err := bson.Marshal(test.body)
			if err != nil {
				t.Fatal(err)
			}
			event, err := Decode(loco.Packet{Header: loco.Header{Method: "NEWMEM"}, Body: body})
			if event != nil || !errors.Is(err, ErrMalformedEvent) {
				t.Fatalf("event=%#v error=%v, want nil and ErrMalformedEvent", event, err)
			}
		})
	}
}
