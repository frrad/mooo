package events

import (
	"testing"

	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDecodeNewMemCarriesChatAndInviteeIdentity(t *testing.T) {
	body, err := bson.Marshal(bson.D{{Key: "chatLog", Value: bson.D{
		{Key: "chatId", Value: int64(42)},
		{Key: "logId", Value: int64(102)},
		{Key: "feed", Value: bson.D{{Key: "invitees", Value: bson.A{
			bson.D{{Key: "userId", Value: int64(7)}, {Key: "userType", Value: int32(1)}},
			bson.D{{Key: "userId", Value: int64(8)}, {Key: "userType", Value: int32(2)}},
		}}}},
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
