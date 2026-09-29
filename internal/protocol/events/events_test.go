package events

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func packet(t *testing.T, method string, body bson.D) loco.Packet {
	t.Helper()
	raw, err := bson.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return loco.Packet{Header: loco.Header{Method: method}, Body: raw}
}

func TestDecodeTextMessage(t *testing.T) {
	event, err := Decode(packet(t, "MSG", bson.D{
		{Key: "chatId", Value: int64(42)}, {Key: "logId", Value: int64(98)},
		{Key: "chatLog", Value: bson.D{
			{Key: "logId", Value: int64(99)}, {Key: "type", Value: int32(1)},
			{Key: "authorId", Value: int64(7)}, {Key: "sendAt", Value: int32(1234)}, {Key: "message", Value: "hello"},
		}},
	}))
	message, ok := event.(TextMessage)
	if err != nil || !ok {
		t.Fatalf("event=%T err=%v", event, err)
	}
	if message.ChatID != 42 || message.LogID != 99 || message.AuthorID != 7 || message.SentAt != 1234 || message.Message != "hello" {
		t.Fatalf("message = %#v", message)
	}
	if strings.Contains(fmt.Sprintf("%v %+v %#v", message, message, message), "hello") {
		t.Fatal("TextMessage.String revealed message content")
	}

	compatible, err := Decode(packet(t, "MSG", bson.D{
		{Key: "chatId", Value: int64(42)}, {Key: "logId", Value: int64(100)},
		{Key: "chatLog", Value: bson.D{{Key: "type", Value: int32(1)}, {Key: "msg", Value: "compatible"}}},
	}))
	compatibleMessage, ok := compatible.(TextMessage)
	if err != nil || !ok || compatibleMessage.LogID != 100 || compatibleMessage.Message != "compatible" {
		t.Fatalf("compatible event=%T value=%#v err=%v", compatible, compatible, err)
	}
}

func TestDecodePhotoAndUnsupportedMessage(t *testing.T) {
	attachment := `{"k":"opaque","w":3,"h":2,"s":12,"cs":"0123456789ABCDEF0123456789ABCDEF01234567","mt":"image/jpg","url":"https://talk.kakaocdn.net/file?sig=fixture","thumbnailUrl":"https://talk.kakaocdn.net/thumb?sig=fixture","expire":1}`
	photoEvent, err := Decode(packet(t, "MSG", bson.D{
		{Key: "chatId", Value: int64(42)},
		{Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "type", Value: int32(2)}, {Key: "attachment", Value: attachment}}},
	}))
	photo, ok := photoEvent.(PhotoMessage)
	if err != nil || !ok || photo.Message.Attachment.Width != 3 {
		t.Fatalf("event=%T value=%#v err=%v", photoEvent, photoEvent, err)
	}
	if strings.Contains(fmt.Sprintf("%v %+v %#v", photo, photo, photo), "opaque") {
		t.Fatal("PhotoMessage.String revealed attachment data")
	}

	unsupportedEvent, err := Decode(packet(t, "MSG", bson.D{
		{Key: "chatId", Value: int64(42)},
		{Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(100)}, {Key: "type", Value: int32(99)}}},
	}))
	unsupported, ok := unsupportedEvent.(UnsupportedMessage)
	if err != nil || !ok || unsupported.Type != 99 {
		t.Fatalf("event=%T value=%#v err=%v", unsupportedEvent, unsupportedEvent, err)
	}
}

func TestDecodeUnknownAndMalformed(t *testing.T) {
	event, err := Decode(packet(t, "KICKOUT", bson.D{{Key: "reason", Value: "synthetic"}}))
	unknown, ok := event.(UnknownPacket)
	if err != nil || !ok || unknown.Method != "KICKOUT" {
		t.Fatalf("event=%T value=%#v err=%v", event, event, err)
	}
	malformed := packet(t, "MSG", bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "type", Value: int32(1)}}}})
	if _, err := Decode(malformed); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("malformed error = %v", err)
	}
}
