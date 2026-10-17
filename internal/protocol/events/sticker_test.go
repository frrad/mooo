package events

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
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

// Regression (owned observation, 2026-10-16): a sticker sent with text is one
// type-12 log whose message is the text. The decoder dropped the message, so
// only the sticker reached Matrix.
func TestObservedStickerWithTextKeepsMessage(t *testing.T) {
	raw, err := os.ReadFile("../../../research/fixtures/stickers/observed-direct-sticker-with-text.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Type       int32          `json:"type"`
		Message    string         `json:"message"`
		Attachment map[string]any `json:"attachment"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	attachment, err := json.Marshal(fixture.Attachment)
	if err != nil {
		t.Fatal(err)
	}
	p := packet(t, "MSG", bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{
		{Key: "logId", Value: int64(99)}, {Key: "authorId", Value: int64(8)}, {Key: "type", Value: fixture.Type},
		{Key: "message", Value: fixture.Message}, {Key: "attachment", Value: string(attachment)},
	}}})
	e, err := Decode(p)
	if err != nil {
		t.Fatal(err)
	}
	sticker, ok := e.(StickerMessage)
	if !ok {
		t.Fatalf("decoded %T, want StickerMessage", e)
	}
	if sticker.Message != "mooo-sticker-text-1" {
		t.Fatalf("sticker message = %q, want the sent text", sticker.Message)
	}
	if sticker.Attachment.Path != "synthetic/emot_direct_001.png" {
		t.Fatalf("sticker path = %q", sticker.Attachment.Path)
	}
}

func TestStickerWithInvalidMessageIsRecoverableGap(t *testing.T) {
	p := packet(t, "MSG", bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{
		{Key: "logId", Value: int64(99)}, {Key: "type", Value: int32(12)},
		{Key: "message", Value: "synthetic\x00text"},
		{Key: "attachment", Value: `{"path":"synthetic/001.png","name":"(Emoticons)","type":"image/png"}`},
	}}})
	e, err := DecodeForDelivery(loco.Packet(p))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e.(MessageGap); !ok {
		t.Fatalf("decoded %T, want MessageGap for a sticker message with a NUL", e)
	}
}
