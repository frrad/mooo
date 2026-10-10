package chatmeta

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestChatOnRoomRequestUsesMacKeysAndTypes(t *testing.T) {
	body, err := (ChatOnRoomRequest{ChatID: 42}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(body)
	if v := raw.Lookup("chatId"); v.Type != bson.TypeInt64 || v.Int64() != 42 {
		t.Fatalf("chatId = %v", v)
	}
	// A room without a stored token sends zero; regular groups send opt 0.
	if v := raw.Lookup("token"); v.Type != bson.TypeInt64 || v.Int64() != 0 {
		t.Fatalf("token = %v", v)
	}
	if v := raw.Lookup("opt"); v.Type != bson.TypeInt32 || v.Int32() != 0 {
		t.Fatalf("opt = %v", v)
	}
	if _, err := (ChatOnRoomRequest{}).MarshalBSON(); err == nil {
		t.Fatal("zero chat accepted")
	}
}

func TestDecodeChatOnRoomPairsWatermarksWithActiveMembers(t *testing.T) {
	body, _ := bson.Marshal(bson.D{
		{Key: "status", Value: int32(0)}, {Key: "c", Value: int64(42)}, {Key: "f", Value: true}, {Key: "o", Value: int64(9)},
		{Key: "l", Value: int64(105)}, {Key: "a", Value: bson.A{int64(7), int32(8)}}, {Key: "w", Value: bson.A{int64(104), int64(100)}},
		{Key: "mi", Value: bson.A{int64(8), int64(7), int64(9)}},
	})
	r, err := DecodeChatOnRoomResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if r.ChatID != 42 || !r.Full || r.Token != 9 || r.LastLogID != 105 || len(r.Watermarks) != 2 || r.Watermarks[7] != 104 || r.Watermarks[8] != 100 {
		t.Fatalf("response = %+v", r)
	}
	mismatch, _ := bson.Marshal(bson.D{{Key: "c", Value: int64(42)}, {Key: "a", Value: bson.A{int64(7)}}, {Key: "w", Value: bson.A{int64(1), int64(2)}}})
	if _, err := DecodeChatOnRoomResponse(mismatch); err == nil {
		t.Fatal("unpaired watermarks accepted")
	}
	noArrays, _ := bson.Marshal(bson.D{{Key: "c", Value: int64(42)}})
	if r, err := DecodeChatOnRoomResponse(noArrays); err != nil || len(r.Watermarks) != 0 {
		t.Fatalf("response without watermarks = %+v %v", r, err)
	}
}
