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

func TestDecodeReplyMessage(t *testing.T) {
	event, err := Decode(packet(t, "MSG", bson.D{
		{Key: "chatId", Value: int64(42)},
		{Key: "chatLog", Value: bson.D{
			{Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(26)},
			{Key: "authorId", Value: int64(8)}, {Key: "sendAt", Value: int32(1235)}, {Key: "message", Value: "reply text"},
			{Key: "attachment", Value: `{"src_logId":99,"src_userId":7,"src_type":1,"src_message":"source text","src_spoilers":[]}`},
		}},
	}))
	reply, ok := event.(ReplyMessage)
	if err != nil || !ok {
		t.Fatalf("event=%T err=%v", event, err)
	}
	if reply.ChatID != 42 || reply.LogID != 101 || reply.AuthorID != 8 || reply.Message != "reply text" || reply.Source.LogID != 99 || reply.Source.UserID != 7 || reply.Source.Type != 1 || reply.Source.Message != "source text" {
		t.Fatalf("reply = %#v", reply)
	}
	formatted := fmt.Sprintf("%v %+v %#v", reply, reply, reply)
	if strings.Contains(formatted, "reply text") || strings.Contains(formatted, "source text") {
		t.Fatal("ReplyMessage formatting revealed message content")
	}
}

func TestDecodeReactionChanged(t *testing.T) {
	event, err := Decode(packet(t, "CHGLOGMETA", bson.D{
		{Key: "logId", Value: int64(99)}, {Key: "type", Value: int32(2)}, {Key: "chatId", Value: int64(42)},
		{Key: "content", Value: `{"rx":[{"a":{"ko":"synthetic label"},"c":2,"k":2,"o":"1200509"}]}`},
		{Key: "revision", Value: int64(3)}, {Key: "linkId", Value: int64(88)}, {Key: "extra", Value: nil},
	}))
	changed, ok := event.(ReactionChanged)
	if err != nil || !ok {
		t.Fatalf("event=%T err=%v", event, err)
	}
	if changed.ChatID != 42 || changed.LinkID != 88 || changed.LogID != 99 || changed.Revision != 3 || len(changed.Items) != 1 || changed.Items[0].ID != "1200509" || changed.Items[0].Count != 2 || changed.Items[0].Kind != 2 {
		t.Fatalf("reaction = %#v", changed)
	}
	if strings.Contains(fmt.Sprintf("%v %+v %#v", changed, changed, changed), "synthetic label") {
		t.Fatal("ReactionChanged formatting revealed content")
	}

	unsupported, err := Decode(packet(t, "CHGLOGMETA", bson.D{
		{Key: "logId", Value: int64(99)}, {Key: "type", Value: int32(7)}, {Key: "chatId", Value: int64(42)},
	}))
	meta, ok := unsupported.(UnsupportedLogMeta)
	if err != nil || !ok || meta.Type != 7 {
		t.Fatalf("unsupported=%T value=%#v err=%v", unsupported, unsupported, err)
	}
}

func TestDecodeReadStateChanged(t *testing.T) {
	event, err := Decode(packet(t, "DECUNREAD", bson.D{
		{Key: "chatId", Value: int64(42)},
		{Key: "userId", Value: int64(7)},
		{Key: "watermark", Value: int64(99)},
	}))
	changed, ok := event.(ReadStateChanged)
	if err != nil || !ok {
		t.Fatalf("event=%T err=%v", event, err)
	}
	if changed.ChatID != 42 || changed.UserID != 7 || changed.Watermark != 99 || changed.Kind() != KindReadStateChanged {
		t.Fatalf("read state = %#v", changed)
	}
	if _, _, ok := MessagePosition(changed); ok {
		t.Fatal("read-state watermark treated as a message commit position")
	}

	for name, body := range map[string]bson.D{
		"missing member": {{Key: "chatId", Value: int64(42)}, {Key: "watermark", Value: int64(99)}},
		"zero watermark": {{Key: "chatId", Value: int64(42)}, {Key: "userId", Value: int64(7)}, {Key: "watermark", Value: int64(0)}},
		"wrong type":     {{Key: "chatId", Value: int64(42)}, {Key: "userId", Value: "7"}, {Key: "watermark", Value: int64(99)}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(packet(t, "DECUNREAD", body)); !errors.Is(err, ErrMalformedEvent) {
				t.Fatalf("malformed error = %v", err)
			}
		})
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
	for name, malformed := range map[string]loco.Packet{
		"reply trailing JSON": packet(t, "MSG", bson.D{
			{Key: "chatId", Value: int64(42)},
			{Key: "chatLog", Value: bson.D{
				{Key: "logId", Value: int64(99)}, {Key: "type", Value: int32(26)}, {Key: "message", Value: "reply"},
				{Key: "attachment", Value: `{"src_logId":98,"src_userId":7,"src_type":1,"src_message":"source"}{}`},
			}},
		}),
		"reaction trailing JSON": packet(t, "CHGLOGMETA", bson.D{
			{Key: "chatId", Value: int64(42)}, {Key: "logId", Value: int64(99)}, {Key: "type", Value: int32(2)},
			{Key: "revision", Value: int64(3)}, {Key: "content", Value: `{"rx":[]}[]`},
		}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode(malformed); !errors.Is(err, ErrMalformedEvent) {
				t.Fatalf("malformed error = %v", err)
			}
		})
	}
}
