package syncmsg

import (
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestRequestWireShape(t *testing.T) {
	body, err := (Request{ChatID: 42, Cur: 100, Max: 900, Count: 300}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	elements, err := bson.Raw(body).Elements()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"chatId", "cur", "max", "cnt"}
	for i, element := range elements {
		if element.Key() != want[i] {
			t.Fatalf("key %d = %q", i, element.Key())
		}
	}
	if bson.Raw(body).Lookup("chatId").Type != bson.TypeInt64 || bson.Raw(body).Lookup("cur").Type != bson.TypeInt64 || bson.Raw(body).Lookup("max").Type != bson.TypeInt64 || bson.Raw(body).Lookup("cnt").Type != bson.TypeInt32 {
		t.Fatal("request widths changed")
	}
}

func TestRequestRejectsUnboundedOrEmptyRange(t *testing.T) {
	for _, request := range []Request{
		{ChatID: 0, Cur: 1, Max: 2, Count: 1},
		{ChatID: 1, Cur: 2, Max: 2, Count: 1},
		{ChatID: 1, Cur: 1, Max: 2, Count: 301},
	} {
		if _, err := request.MarshalBSON(); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("request %#v error = %v", request, err)
		}
	}
}

func TestParseResponseRequiresOrderedLogIDs(t *testing.T) {
	body, err := bson.Marshal(bson.D{
		{Key: "status", Value: int32(0)},
		{Key: "chatLogs", Value: bson.A{
			bson.D{{Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(1)}},
			bson.D{{Key: "logId", Value: int64(105)}, {Key: "type", Value: int32(2)}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := ParseResponse(body)
	if err != nil || len(response.ChatLogs) != 2 {
		t.Fatalf("response = (%d, %v)", len(response.ChatLogs), err)
	}

	bad, _ := bson.Marshal(bson.D{{Key: "chatLogs", Value: bson.A{
		bson.D{{Key: "logId", Value: int64(105)}},
		bson.D{{Key: "logId", Value: int64(101)}},
	}}})
	if _, err := ParseResponse(bad); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("unordered error = %v", err)
	}
}

func TestTargetFromChatData(t *testing.T) {
	body, err := bson.Marshal(bson.D{
		{Key: "c", Value: int64(42)},
		{Key: "l", Value: bson.D{{Key: "logId", Value: int64(105)}, {Key: "chatId", Value: int64(42)}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := TargetFromChatData(body)
	if err != nil || target.ChatID != 42 || target.MaxLogID != 105 {
		t.Fatalf("target = (%#v, %v)", target, err)
	}
	mismatch, _ := bson.Marshal(bson.D{
		{Key: "c", Value: int64(42)},
		{Key: "l", Value: bson.D{{Key: "logId", Value: int64(105)}, {Key: "chatId", Value: int64(43)}}},
	})
	if _, err := TargetFromChatData(mismatch); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("mismatch error = %v", err)
	}
}
