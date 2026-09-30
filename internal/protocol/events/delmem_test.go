package events

import (
	"testing"

	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDecodeDelMemCarriesChatAndDepartedMemberIdentity(t *testing.T) {
	body, err := bson.Marshal(bson.D{{Key: "chatLog", Value: bson.D{
		{Key: "chatId", Value: int64(42)},
		{Key: "logId", Value: int64(101)},
		{Key: "feed", Value: bson.D{{Key: "leaver", Value: bson.D{
			{Key: "userId", Value: int64(7)},
			{Key: "userType", Value: int32(1)},
		}}}},
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
