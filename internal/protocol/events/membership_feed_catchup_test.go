package events

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Synthetic values in the shapes observed on 2026-10-14 in an owned group's
// catch-up: an offline leave (feedType 2) and a re-invite (feedType 1).
func TestCatchUpMembershipFeedsDecodeAsMembershipEvents(t *testing.T) {
	leave := bson.D{{Key: "logId", Value: int64(201)}, {Key: "prevId", Value: int64(200)}, {Key: "type", Value: int32(0)}, {Key: "authorId", Value: int64(4000)}, {Key: "sendAt", Value: int32(1)},
		{Key: "message", Value: `{"feedType":2,"member":{"userId":4000,"nickName":"Synthetic"},"kicked":false,"hidden":false,"memorial":false}`}}
	invite := bson.D{{Key: "logId", Value: int64(202)}, {Key: "prevId", Value: int64(201)}, {Key: "type", Value: int32(0)}, {Key: "authorId", Value: int64(2000)}, {Key: "sendAt", Value: int32(2)},
		{Key: "message", Value: `{"feedType":1,"inviter":{"userId":2000,"nickName":"Inviter"},"members":[{"userId":4000,"nickName":"Synthetic"}],"byLink":false}`}}
	raw, _ := bson.Marshal(leave)
	evt, err := DecodeChatLog(5000, raw)
	if removed, ok := evt.(MemberRemoved); err != nil || !ok || removed != (MemberRemoved{ChatID: 5000, LogID: 201, UserID: 4000}) {
		t.Fatalf("leave feed = %#v, %v", evt, err)
	}
	raw, _ = bson.Marshal(invite)
	evt, err = DecodeChatLog(5000, raw)
	added, ok := evt.(MemberAdded)
	if err != nil || !ok || added.ChatID != 5000 || added.LogID != 202 || len(added.Members) != 1 || added.Members[0].UserID != 4000 {
		t.Fatalf("invite feed = %#v, %v", evt, err)
	}
	if chatID, logID, ok := MessagePosition(added); !ok || chatID != 5000 || logID != 202 {
		t.Fatal("membership feed is not positioned for commit")
	}
}
