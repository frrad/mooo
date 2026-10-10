package chatlog

import (
	"errors"
	"math"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func marshal(t *testing.T, document bson.D) []byte {
	t.Helper()
	body, err := bson.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestDecodeReadsEnvelopeFromChatLog(t *testing.T) {
	body := marshal(t, bson.D{
		{Key: "chatId", Value: int32(42)},
		{Key: "chatLog", Value: bson.D{
			{Key: "logId", Value: int64(99)},
			{Key: "type", Value: int32(1)},
			{Key: "authorId", Value: int64(7)},
			{Key: "sendAt", Value: int32(1234)},
			{Key: "message", Value: "synthetic"},
		}},
	})
	envelope, err := Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.ChatID != 42 || envelope.LogID != 99 || envelope.Type != 1 || envelope.AuthorID != 7 || envelope.SentAt != 1234 || !envelope.LogIDInChatLog {
		t.Fatalf("envelope = %+v", envelope)
	}
	if message := envelope.Log.Lookup("message").StringValue(); message != "synthetic" {
		t.Fatalf("Log.message = %q", message)
	}
}

func TestDecodeFallsBackToTopLevelLogID(t *testing.T) {
	tests := []struct {
		name     string
		chatLog  bson.D
		wantType int32
	}{
		{name: "missing inner logId", chatLog: bson.D{{Key: "type", Value: int32(2)}}, wantType: 2},
		{name: "string inner logId", chatLog: bson.D{{Key: "logId", Value: "99"}, {Key: "type", Value: int32(2)}}, wantType: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := marshal(t, bson.D{
				{Key: "chatId", Value: int64(42)},
				{Key: "logId", Value: int64(100)},
				{Key: "chatLog", Value: test.chatLog},
			})
			envelope, err := Decode(body)
			if err != nil {
				t.Fatal(err)
			}
			if envelope.LogID != 100 || envelope.LogIDInChatLog || envelope.Type != test.wantType {
				t.Fatalf("envelope = %+v; want top-level logId 100", envelope)
			}
		})
	}
}

func TestDecodeKeepsInvalidInnerLogIDWithoutFallback(t *testing.T) {
	body := marshal(t, bson.D{
		{Key: "chatId", Value: int64(42)},
		{Key: "logId", Value: int64(100)},
		{Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(0)}, {Key: "type", Value: int32(1)}}},
	})
	if _, err := Decode(body); !errors.Is(err, ErrMalformed) {
		t.Fatalf("Decode with zero inner logId = %v; want ErrMalformed", err)
	}
}

func TestDecodeRejectsMalformedEnvelopes(t *testing.T) {
	validLog := bson.D{{Key: "logId", Value: int64(99)}, {Key: "type", Value: int32(1)}}
	tests := []struct {
		name     string
		document bson.D
	}{
		{name: "missing chatId", document: bson.D{{Key: "chatLog", Value: validLog}}},
		{name: "zero chatId", document: bson.D{{Key: "chatId", Value: int64(0)}, {Key: "chatLog", Value: validLog}}},
		{name: "double chatId", document: bson.D{{Key: "chatId", Value: 42.0}, {Key: "chatLog", Value: validLog}}},
		{name: "missing chatLog", document: bson.D{{Key: "chatId", Value: int64(42)}}},
		{name: "chatLog not a document", document: bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: "x"}}},
		{name: "no logId anywhere", document: bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "type", Value: int32(1)}}}}},
		{name: "missing type", document: bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}}}}},
		{name: "negative type", document: bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "type", Value: int32(-1)}}}}},
		{name: "type above int32", document: bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "type", Value: int64(math.MaxInt32) + 1}}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Decode(marshal(t, test.document)); !errors.Is(err, ErrMalformed) {
				t.Fatalf("Decode = %v; want ErrMalformed", err)
			}
		})
	}
	if _, err := Decode([]byte{1, 2, 3}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("Decode(invalid BSON) = %v; want ErrMalformed", err)
	}
}

func TestDecodeTreatsWrongTypeAttributionAsZero(t *testing.T) {
	body := marshal(t, bson.D{
		{Key: "chatId", Value: int64(42)},
		{Key: "chatLog", Value: bson.D{
			{Key: "logId", Value: int64(99)},
			{Key: "type", Value: int32(1)},
			{Key: "authorId", Value: "7"},
			{Key: "sendAt", Value: 1234.0},
		}},
	})
	envelope, err := Decode(body)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.AuthorID != 0 || envelope.SentAt != 0 {
		t.Fatalf("envelope attribution = %d, %d; want zero", envelope.AuthorID, envelope.SentAt)
	}
}
