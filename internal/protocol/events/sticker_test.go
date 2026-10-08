package events

import (
	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
	"testing"
)

func TestObservedStickerIsTypedInsteadOfUnsupported(t *testing.T) {
	p := packet(t, "MSG", bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "authorId", Value: int64(8)}, {Key: "type", Value: int32(20)}, {Key: "attachment", Value: `{"path":"synthetic/001.webp","name":"synthetic_001","type":"sticker","emoticonItemPath":"synthetic/001.webp"}`}}}})
	e, err := Decode(p)
	if err != nil {
		t.Fatal(err)
	}
	sticker, typed := e.(StickerMessage)
	if !typed || sticker.Type != 20 || sticker.AuthorID != 8 || sticker.Attachment.Path != "synthetic/001.webp" {
		t.Fatal("observed sticker lost as unsupported message")
	}
	chat, log, ok := MessagePosition(e)
	if !ok || chat != 42 || log != 99 {
		t.Fatal("sticker cursor lost")
	}
}

func TestMalformedStickerIsRecoverableGap(t *testing.T) {
	p := packet(t, "MSG", bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "type", Value: int32(20)}, {Key: "attachment", Value: `{"path":"../private.webp"}`}}}})
	e, err := DecodeForDelivery(loco.Packet(p))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e.(MessageGap); !ok {
		t.Fatal("invalid sticker should become explicit gap")
	}
}
