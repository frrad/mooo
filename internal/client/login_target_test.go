package client

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Regression (owned acceptance, 2026-10-10): LOGINLIST l stayed at the
// edited message while ll covered an edit feed made while the bridge was
// offline, so catch-up never reached the edit.
func TestLoginChatTargetReachesFeedsAfterTheLastChatLog(t *testing.T) {
	raw, err := bson.Marshal(bson.D{{Key: "c", Value: int64(42)}, {Key: "ll", Value: int64(102)},
		{Key: "l", Value: bson.D{{Key: "chatId", Value: int64(42)}, {Key: "logId", Value: int64(100)}}}})
	if err != nil {
		t.Fatal(err)
	}
	target, err := loginChatTarget(raw)
	if err != nil || target.MaxLogID != 102 {
		t.Fatalf("target = %+v err=%v", target, err)
	}
}
