package chat

import (
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestModifyRequestCarriesTheOriginalTypeAndNewText(t *testing.T) {
	body, err := (ModifyRequest{ChatID: 42, LogID: 101, Type: TextType, Message: "synthetic edit", Extra: "{}"}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(body)
	if raw.Lookup("chatId").Int64() != 42 || raw.Lookup("logId").Int64() != 101 || raw.Lookup("type").Int32() != TextType ||
		raw.Lookup("msg").StringValue() != "synthetic edit" || raw.Lookup("extra").StringValue() != "{}" {
		t.Fatalf("MODIFYMSG = %v", raw)
	}
	for _, bad := range []ModifyRequest{
		{ChatID: 42, LogID: 101, Type: TextType, Message: "", Extra: "{}"},
		{ChatID: 0, LogID: 101, Type: TextType, Message: "x", Extra: "{}"},
		{ChatID: 42, LogID: 0, Type: TextType, Message: "x", Extra: "{}"},
		{ChatID: 42, LogID: 101, Type: TextType, Message: "x", Extra: "not json"},
	} {
		if _, err := bad.MarshalBSON(); !errors.Is(err, ErrInvalidMessage) && !errors.Is(err, ErrInvalidChatID) && !errors.Is(err, ErrInvalidMessageID) {
			t.Fatalf("%+v accepted: %v", bad, err)
		}
	}
}

func TestDecodeModifyResponseReturnsTheNewRevision(t *testing.T) {
	body, _ := bson.Marshal(bson.D{{Key: "status", Value: int32(0)},
		{Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(102)}, {Key: "type", Value: int32(0)}}},
		{Key: "modifiedChatLog", Value: bson.D{{Key: "logId", Value: int64(101)}, {Key: "revision", Value: int32(2)}, {Key: "message", Value: "synthetic edit"}}},
	})
	revision, err := DecodeModifyResponse(body, 101)
	if err != nil || revision != 2 {
		t.Fatalf("revision=%d err=%v", revision, err)
	}
	if _, err := DecodeModifyResponse(body, 999); err == nil {
		t.Fatal("response for another message accepted")
	}
}

func TestDeleteRequestNamesOneMessage(t *testing.T) {
	body, err := (DeleteRequest{ChatID: 42, LogID: 101}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(body)
	if raw.Lookup("chatId").Int64() != 42 || raw.Lookup("logId").Int64() != 101 {
		t.Fatalf("DELETEMSG = %v", raw)
	}
	elems, _ := raw.Elements()
	if len(elems) != 2 {
		t.Fatalf("DELETEMSG has %d fields", len(elems))
	}
}
