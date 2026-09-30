package events

import (
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
