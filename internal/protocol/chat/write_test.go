package chat

import (
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestWriteRequestMarshalText(t *testing.T) {
	body, err := (WriteRequest{
		ChatID: 42, Message: "hello", Type: TextType,
	}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(body)
	for key, typ := range map[string]bson.Type{
		"chatId": bson.TypeInt64, "msg": bson.TypeString, "type": bson.TypeInt32,
		"noSeen": bson.TypeBoolean,
	} {
		if got := raw.Lookup(key).Type; got != typ {
			t.Fatalf("%s type = %v, want %v", key, got, typ)
		}
	}
	if raw.Lookup("msg").StringValue() != "hello" || raw.Lookup("noSeen").Boolean() {
		t.Fatal("text WRITE values not preserved")
	}
	for _, key := range []string{"msgId", "scope", "threadId", "noLight", "extra", "supplement", "silence", "featureStat"} {
		if _, err := raw.LookupErr(key); err == nil {
			t.Fatalf("empty %s must be omitted", key)
		}
	}
}

func TestWriteRequestValidation(t *testing.T) {
	base := WriteRequest{ChatID: 1, Message: "ok", Type: TextType}
	for name, tc := range map[string]struct {
		request WriteRequest
		want    error
	}{
		"chat id":    {func() WriteRequest { r := base; r.ChatID = 0; return r }(), ErrInvalidChatID},
		"message id": {func() WriteRequest { r := base; r.MessageID = -1; return r }(), ErrInvalidMessageID},
		"type":       {func() WriteRequest { r := base; r.Type = 0; return r }(), ErrInvalidWriteType},
		"empty":      {func() WriteRequest { r := base; r.Message = ""; return r }(), ErrInvalidMessage},
		"nul":        {func() WriteRequest { r := base; r.Message = "a\x00b"; return r }(), ErrInvalidMessage},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tc.request.MarshalBSON()
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDecodeWriteResponse(t *testing.T) {
	chatLog, err := bson.Marshal(bson.D{{Key: "msg", Value: "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := bson.Marshal(bson.D{
		{Key: "msgId", Value: int64(7)}, {Key: "chatId", Value: int64(42)},
		{Key: "logId", Value: int64(99)}, {Key: "prevId", Value: int64(98)},
		{Key: "sendAt", Value: int32(1234)}, {Key: "chatLog", Value: bson.Raw(chatLog)},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := DecodeWriteResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if response.MessageID != 7 || response.ChatID != 42 || response.LogID != 99 || response.PrevID != 98 || response.SendAt != 1234 {
		t.Fatalf("unexpected response: %#v", response)
	}
}
