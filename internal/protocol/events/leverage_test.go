package events

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func leveragePacket(t *testing.T, message, attachment *string) bson.D {
	t.Helper()
	log := bson.D{
		{Key: "logId", Value: int64(99)},
		{Key: "authorId", Value: int64(7)},
		{Key: "type", Value: messagetype.Leverage},
		{Key: "sendAt", Value: int32(1700000000)},
	}
	if message != nil {
		log = append(log, bson.E{Key: "message", Value: *message})
	}
	if attachment != nil {
		log = append(log, bson.E{Key: "attachment", Value: *attachment})
	}
	return bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: log}}
}

func decodeLeveragePacket(t *testing.T, message, attachment *string) Event {
	t.Helper()
	e, err := Decode(packet(t, "MSG", leveragePacket(t, message, attachment)))
	if err != nil {
		t.Fatal(err)
	}
	if chatID, logID, ok := MessagePosition(e); !ok || chatID != 42 || logID != 99 {
		t.Fatalf("%T lost its cursor", e)
	}
	return e
}

func requireLeverage(t *testing.T, e Event) LeverageMessage {
	t.Helper()
	card, ok := e.(LeverageMessage)
	if !ok {
		t.Fatalf("type 71 decoded as %T", e)
	}
	if card.ChatID != 42 || card.LogID != 99 || card.AuthorID != 7 || card.SentAt != 1700000000 {
		t.Fatalf("card identity = chat %d log %d author %d sent %d", card.ChatID, card.LogID, card.AuthorID, card.SentAt)
	}
	return card
}

// Cards shared and forwarded between owned accounts decode to the chat log
// message and the card's link (research/fixtures/leverage/observed-cards.json).
func TestObservedLeverageCardsDecode(t *testing.T) {
	var fixture struct {
		Cards []struct {
			Name       string `json:"name"`
			Type       int32  `json:"type"`
			Message    string `json:"message"`
			Attachment string `json:"attachment"`
			Expected   struct {
				MoooLink string `json:"mooo_link"`
				Reply    bool   `json:"reply"`
			} `json:"expected"`
		} `json:"cards"`
	}
	b, err := os.ReadFile("../../../research/fixtures/leverage/observed-cards.json")
	if err != nil || json.Unmarshal(b, &fixture) != nil || len(fixture.Cards) == 0 {
		t.Fatal("observed leverage fixture unreadable")
	}
	for _, c := range fixture.Cards {
		if c.Type != messagetype.Leverage {
			t.Fatalf("%s: fixture type %d", c.Name, c.Type)
		}
		card := requireLeverage(t, decodeLeveragePacket(t, &c.Message, &c.Attachment))
		if card.Summary != c.Message || card.Link != c.Expected.MoooLink || card.ReplyUnavailable == c.Expected.Reply {
			t.Errorf("%s: summary %q link %q replyUnavailable %v", c.Name, card.Summary, card.Link, card.ReplyUnavailable)
		}
	}
}

// The decoder keeps what the official Mac model shows: the chat log message
// as the summary, the card's tap link, and whether Reply is offered
// (research/fixtures/leverage/executed-mac-model.json).
func TestExecutedLeverageCardsDecodeLikeTheMacModel(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name       string  `json:"name"`
			Message    *string `json:"message"`
			Attachment *string `json:"attachment"`
			Expected   struct {
				Crashed           bool    `json:"crashed"`
				Summary           *string `json:"summary"`
				ReplyAvailable    bool    `json:"reply_available"`
				ReactionAvailable bool    `json:"reaction_available"`
				PublicLink        *string `json:"public_link"`
			} `json:"expected"`
			Deviation    string `json:"mooo_deviation"`
			MoooExpected *struct {
				Kind     string  `json:"kind"`
				Summary  string  `json:"summary"`
				Link     *string `json:"link"`
				Reply    bool    `json:"reply"`
				Reaction bool    `json:"reaction"`
			} `json:"mooo_expected"`
		} `json:"cases"`
	}
	b, err := os.ReadFile("../../../research/fixtures/leverage/executed-mac-model.json")
	if err != nil || json.Unmarshal(b, &fixture) != nil || len(fixture.Cases) < 80 {
		t.Fatal("executed leverage fixture unreadable")
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			kind, summary, link, reply, reaction := "leverage", "", "", c.Expected.ReplyAvailable, c.Expected.ReactionAvailable
			if c.Expected.Summary != nil {
				summary = *c.Expected.Summary
			}
			if c.Expected.PublicLink != nil {
				link = *c.Expected.PublicLink
			}
			if (c.MoooExpected == nil) != (c.Deviation == "") || c.Expected.Crashed && c.MoooExpected == nil {
				t.Fatal("a deviation needs both mooo_deviation and mooo_expected")
			}
			if m := c.MoooExpected; m != nil {
				kind, summary, link, reply, reaction = m.Kind, m.Summary, "", m.Reply, m.Reaction
				if m.Link != nil {
					link = *m.Link
				}
			}
			e := decodeLeveragePacket(t, c.Message, c.Attachment)
			if kind == "unsupported" {
				if got, ok := e.(UnsupportedMessage); !ok || got.Type != messagetype.Leverage {
					t.Fatalf("decoded %T, want the unsupported notice", e)
				}
				return
			}
			card := requireLeverage(t, e)
			if card.Summary != summary || card.Link != link || card.ReplyUnavailable == reply || card.ReactionUnavailable == reaction {
				t.Fatalf("summary %q link %q replyUnavailable %v reactionUnavailable %v; want %q %q reply %v reaction %v", card.Summary, card.Link, card.ReplyUnavailable, card.ReactionUnavailable, summary, link, reply, reaction)
			}
		})
	}
}

// A message that is not valid UTF-8, is too long, or is missing never
// reaches Matrix as text.
func TestLeverageWithUnusableMessageIsUnsupported(t *testing.T) {
	attachment := `{"P":{"L":{"LPC":"https://example.com/card"}}}`
	for name, message := range map[string]*string{
		"invalid UTF-8": ptr("summary \xff"),
		"NUL":           ptr("summary\x00"),
		"too long":      ptr(strings.Repeat("x", maxLeverageText+1)),
		"missing":       nil,
	} {
		if e := decodeLeveragePacket(t, message, &attachment); e.Kind() != KindUnsupportedMessage {
			t.Errorf("%s: decoded %T", name, e)
		}
	}
}

// An attachment that is oversized, not valid UTF-8, or followed by trailing
// data is treated as unreadable: the summary stays, the link goes.
func TestLeverageUnreadableAttachmentKeepsSummaryOnly(t *testing.T) {
	message := "Synthetic card summary"
	valid := `{"P":{"L":{"LPC":"https://example.com/card"}}}`
	if card := requireLeverage(t, decodeLeveragePacket(t, &message, &valid)); card.Link != "https://example.com/card" {
		t.Fatalf("control link = %q", card.Link)
	}
	for name, attachment := range map[string]string{
		"invalid UTF-8": `{"P":{"L":{"LPC":"https://example.com/\xff"}}}`,
		"trailing data": valid + ` {}`,
		"oversized":     `{"P":{"L":{"LPC":"https://example.com/card"}},"pad":"` + strings.Repeat("x", maxLeverageAttachment) + `"}`,
	} {
		card := requireLeverage(t, decodeLeveragePacket(t, &message, &attachment))
		if card.Summary != message || card.Link != "" || card.ReplyUnavailable {
			t.Errorf("%s: summary %q link %q replyUnavailable %v", name, card.Summary, card.Link, card.ReplyUnavailable)
		}
	}
}

// Links that are not plain http(s) URLs, or that are too long, are dropped.
func TestLeverageLinkMustBeAPlainWebURL(t *testing.T) {
	message := "Synthetic card summary"
	for link, want := range map[string]string{
		"https://example.com/card":         "https://example.com/card",
		"HTTPS://example.com/card":         "HTTPS://example.com/card",
		"https://":                         "",
		"https:example.com":                "",
		"ftp://example.com/card":           "",
		"javascript:alert(1)":              "",
		"https://example.com/\tcard":       "",
		"https://example.com/" + "x" + " ": "",
		"https://example.com/" + strings.Repeat("x", maxLeverageLink): "",
	} {
		attachment, _ := json.Marshal(map[string]any{"P": map[string]any{"L": map[string]any{"LPC": link}}})
		value := string(attachment)
		if card := requireLeverage(t, decodeLeveragePacket(t, &message, &value)); card.Link != want {
			t.Errorf("link %q bridged as %q, want %q", link, card.Link, want)
		}
	}
}

// LOCK, ADULT and PC read their values as NSString/NSNumber boolValue does.
func TestLeverageBooleanFlags(t *testing.T) {
	message := "Synthetic card summary"
	for value, want := range map[string]bool{
		`true`: true, `false`: false, `1`: true, `0`: false, `null`: false, `"+-1"`: false,
		`"Y"`: true, `"yes"`: true, `"t"`: true, `"  T"`: true, `"+1"`: true, `"-1"`: true, `"007"`: true, `"9z"`: true,
		`"no"`: false, `"false"`: false, `"0"`: false, `"000"`: false, `""`: false, `"x"`: false, `[]`: false, `{}`: false,
	} {
		attachment := fmt.Sprintf(`{"P":{"LOCK":%s,"L":{"LPC":"https://example.com/card"}}}`, value)
		card := requireLeverage(t, decodeLeveragePacket(t, &message, &attachment))
		if card.ReplyUnavailable != want || (card.Link == "") != want {
			t.Errorf("LOCK %s: replyUnavailable %v link %q", value, card.ReplyUnavailable, card.Link)
		}
	}
}

func TestLeverageMessageRedactsContent(t *testing.T) {
	card := LeverageMessage{ChatID: 1, LogID: 2, AuthorID: 3, Summary: "private summary", Link: "https://example.com/private"}
	for _, text := range []string{card.String(), card.GoString(), fmt.Sprintf("%v %+v %#v", card, card, card)} {
		if strings.Contains(text, "private") || strings.Contains(text, "example.com") {
			t.Fatalf("leverage message leaked content: %s", text)
		}
	}
}

func ptr(s string) *string { return &s }
