package chatmeta

import (
	"errors"
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// The field names and BSON types follow an observed owned CREATE reply
// (2026-10-15); all values are synthetic.
func TestDecodeChatRoomFromCreateReplyShape(t *testing.T) {
	raw, err := bson.Marshal(bson.D{
		{Key: "chatId", Value: int64(5000)},
		{Key: "members", Value: bson.A{
			bson.D{{Key: "userId", Value: int64(2000)}, {Key: "nickName", Value: "Two"}},
			bson.D{{Key: "userId", Value: int64(4000)}, {Key: "nickName", Value: "Four"}},
		}},
		{Key: "activeMemberIds", Value: bson.A{int64(1000), int64(2000), int64(4000)}},
		{Key: "watermarks", Value: bson.A{int64(0), int64(0), int64(0)}},
		{Key: "lastMessage", Value: nil},
		{Key: "lastUpdatedAt", Value: nil},
		{Key: "lastLogId", Value: int64(77)},
		{Key: "newMessageCount", Value: int32(0)},
		{Key: "type", Value: "MultiChat"},
		{Key: "pushAlert", Value: nil},
		{Key: "metaRevisions", Value: nil},
		{Key: "metas", Value: bson.A{bson.D{{Key: "type", Value: int32(3)}, {Key: "revision", Value: int64(1)}, {Key: "content", Value: "Synthetic group"}}}},
		{Key: "o", Value: int64(0)},
		{Key: "pct", Value: nil},
		{Key: "ii", Value: bson.A{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	room, err := DecodeChatRoom(raw)
	if err != nil {
		t.Fatal(err)
	}
	if room.ChatID != 5000 || room.Type != "MultiChat" || !slices.Equal(room.ActiveMemberIDs, []int64{1000, 2000, 4000}) {
		t.Fatalf("room = %+v", room)
	}
	if len(room.Members) != 2 || room.Members[1].UserID != 4000 || room.Members[1].Nickname != "Four" {
		t.Fatalf("members = %+v", room.Members)
	}
	if len(room.Metas) != 1 || room.Metas[0].Type != 3 || room.Metas[0].Content != "Synthetic group" {
		t.Fatalf("metas = %+v", room.Metas)
	}
}

func TestDecodeChatRoomRejectsInvalidActiveMembers(t *testing.T) {
	for _, active := range []bson.A{{int64(1000), int64(1000)}, {int64(0)}} {
		raw, err := bson.Marshal(bson.D{{Key: "chatId", Value: int64(5000)}, {Key: "type", Value: "MultiChat"}, {Key: "activeMemberIds", Value: active}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = DecodeChatRoom(raw); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("active %v: err = %v", active, err)
		}
	}
}
