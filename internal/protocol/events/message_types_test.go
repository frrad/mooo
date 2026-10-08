package events

import (
	"testing"

	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// Naming a recovered value must not enable an unsupported payload or remove flags.
func TestNamedUnsupportedMessageTypesPreserveIdentity(t *testing.T) {
	for _, typ := range []int32{
		messagetype.Audio,
		messagetype.Contact, messagetype.Location,
		messagetype.AnimatedEmoticon, messagetype.Spritecon, messagetype.AnimatedStickerEx,
		messagetype.Vote, messagetype.Mvoip, messagetype.Universal,
		messagetype.Text | messagetype.DeletedAllChatTypeFlag,
		messagetype.Text | messagetype.OpenLinkIllegalBlindFlag,
		messagetype.Text | messagetype.SecretChatTypeFlag,
		1234567, // Future unknown values remain observable too.
	} {
		p := packet(t, "MSG", bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "type", Value: typ}}}})
		e, err := Decode(p)
		if err != nil {
			t.Fatalf("type %d: %v", typ, err)
		}
		msg, ok := e.(UnsupportedMessage)
		if !ok || msg.Type != typ || msg.ChatID != 42 || msg.LogID != 99 {
			t.Fatalf("type %d lost raw identity: %T", typ, e)
		}
		chatID, logID, positioned := MessagePosition(e)
		if !positioned || chatID != 42 || logID != 99 {
			t.Fatalf("type %d lost cursor", typ)
		}
	}
}
