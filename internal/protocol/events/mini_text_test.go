package events

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestObservedMiniTextDoesNotLoseGraphics(t *testing.T) {
	raw, err := os.ReadFile("../../../research/fixtures/mini-emoticons/observed-text.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name         string
			Message      string
			Attachment   json.RawMessage
			MoooExpected []MiniTextPart `json:"mooo_expected"`
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			p := packet(t, "MSG", bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "type", Value: messagetype.Text}, {Key: "message", Value: tc.Message}, {Key: "attachment", Value: string(tc.Attachment)}}}})
			e, err := DecodeForDelivery(p)
			if err != nil {
				t.Fatal(err)
			}
			m, ok := e.(MiniTextMessage)
			if !ok {
				t.Fatalf("Mini graphic lost: got %T", e)
			}
			if m.Message != tc.Message || !reflect.DeepEqual(m.Parts, tc.MoooExpected) {
				t.Fatal("source, graphic placement or order lost")
			}
			chat, log, identified := MessagePosition(m)
			if !identified || chat != 42 || log != 99 {
				t.Fatal("Mini identity lost")
			}
		})
	}
}

func TestMiniMalformedAttachmentsRetainIdentity(t *testing.T) {
	for _, attachment := range []string{
		`{"emojis":null}`,
		`{"emojis":{"total_item":1,"total_len":6,"items":[{"id":"120_1","len":6,"at":[0]}]}}`,
		`{"emojis":{"total_item":1,"total_len":6,"items":[{"id":"120_1","len":6,"at":[2]}]}}`,
		`{"emojis":{"total_item":2,"total_len":12,"items":[{"id":"120_1","len":6,"at":[1,1]}]}}`,
		`{"emojis":{"total_item":1,"total_len":5,"items":[{"id":"120_1","len":6,"at":[1]}]}}`,
		`{"emojis":{"total_item":1,"total_len":6,"items":[{"id":"../bad_1","len":6,"at":[1]}]}}`,
		`{"emojis":{"total_item":1,"total_len":6,"items":[{"id":"120_1","len":6,"at":[1],"at":[2]}]}}`,
		`{"emojis":{"total_item":1,"total_len":6,"items":[{"id":"120_1","len":6,"at":[1],"unknown":true}]}}`,
	} {
		p := packet(t, "MSG", bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "type", Value: messagetype.Text}, {Key: "message", Value: "(item)"}, {Key: "attachment", Value: attachment}}}})
		e, err := DecodeForDelivery(p)
		gap, ok := e.(MessageGap)
		if err != nil || !ok || gap.ChatID != 42 || gap.LogID != 99 || gap.Type != messagetype.Text {
			t.Fatalf("Mini failure lost identity: %T", e)
		}
	}
	var nilMessage *MiniTextMessage
	if _, _, ok := MessagePosition(nilMessage); ok {
		t.Fatal("nil Mini identified")
	}
}

func TestMiniUTF16AndSourceOrder(t *testing.T) {
	// First fallback includes a surrogate pair; metadata item order differs from
	// source order. No fixture-parity claim is made for these synthetic cases.
	p := packet(t, "MSG", bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "type", Value: messagetype.Text}, {Key: "message", Value: "prefix (😀) / (item)"}, {Key: "attachment", Value: `{"emojis":{"total_item":2,"total_len":10,"items":[{"id":"120_2","len":6,"at":[2]},{"id":"120_1","len":4,"at":[1]}]}}`}}}})
	e, err := DecodeForDelivery(p)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := e.(MiniTextMessage)
	want := []MiniTextPart{{Text: "prefix "}, {Text: "(😀)", ResourceID: "120_1"}, {Text: " / "}, {Text: "(item)", ResourceID: "120_2"}}
	if !ok || !reflect.DeepEqual(m.Parts, want) {
		t.Fatalf("UTF-16 source ordering lost: %T", e)
	}
}

func TestTextNullAttachmentDoesNotInventMiniGap(t *testing.T) {
	p := packet(t, "MSG", bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "type", Value: messagetype.Text}, {Key: "message", Value: "synthetic ordinary text"}, {Key: "attachment", Value: "null"}}}})
	e, err := DecodeForDelivery(p)
	if _, ok := e.(TextMessage); err != nil || !ok {
		t.Fatalf("ordinary null attachment became a Mini gap: %T", e)
	}
}

func TestTextWithoutMiniDoesNotUseMiniArrayLimits(t *testing.T) {
	a := `{"unrelated":[` + strings.Repeat(`0,`, 255) + `0]}`
	p := packet(t, "MSG", bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "type", Value: messagetype.Text}, {Key: "message", Value: "synthetic ordinary text"}, {Key: "attachment", Value: a}}}})
	e, err := DecodeForDelivery(p)
	if _, ok := e.(TextMessage); err != nil || !ok {
		t.Fatalf("unrelated metadata became a Mini gap: %T", e)
	}
}
