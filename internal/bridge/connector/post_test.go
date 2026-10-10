package connector

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/event"
)

func postPacket(t *testing.T, attachment string) loco.Packet {
	t.Helper()
	b, err := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "authorId", Value: testOtherID}, {Key: "type", Value: messagetype.Post}, {Key: "attachment", Value: attachment}}}})
	if err != nil {
		t.Fatal(err)
	}
	return loco.Packet{Header: loco.Header{Method: "MSG"}, Body: b}
}

func TestObservedPostSourceTextIsTyped(t *testing.T) {
	b, err := os.ReadFile("../../../research/fixtures/post/observed-text.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Attachment json.RawMessage `json:"attachment"`
		Native     struct {
			Text string `json:"text"`
		} `json:"native_expected"`
		Expected struct {
			Body string `json:"body"`
		} `json:"mooo_expected"`
	}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	e, err := events.DecodeForDelivery(postPacket(t, string(fixture.Attachment)))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := e.(events.PostMessage)
	if !ok {
		t.Fatalf("observed native post lost as %T", e)
	}
	if m.Text != fixture.Native.Text {
		t.Fatal("native post source text lost")
	}
	for _, positioned := range []events.Event{m, &m} {
		chat, log, ok := events.MessagePosition(positioned)
		if !ok || chat != testChatID || log != 99 {
			t.Fatal("post delivery identity lost")
		}
	}
	got, err := convertPost(context.Background(), nil, nil, m)
	if err != nil || len(got.Parts) != 1 {
		t.Fatal("post conversion failed", err)
	}
	c := got.Parts[0].Content
	if c.MsgType != event.MsgText || c.Body != fixture.Expected.Body || strings.Contains(c.Body, "kakaomoim") || c.Format != "" || got.Parts[0].DBMetadata.(*KakaoMessageMetadata).Type != messagetype.Post {
		t.Fatal("post text/metadata boundary lost")
	}
}

func TestPostStructuredTextPriorityAndOrder(t *testing.T) {
	attachment := `{"os":[{"t":1,"ct":"fallback must not win","jct":"[{\"type\":\"text\",\"text\":\"First\\n\"},{\"type\":\"text\",\"text\":\"<literal> & second\"}]"}]}`
	e, err := events.Decode(postPacket(t, attachment))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := e.(events.PostMessage)
	if !ok || m.Text != "First\n<literal> & second" {
		t.Fatal("structured content precedence/order lost")
	}
	got, err := convertPost(context.Background(), nil, nil, m)
	if err != nil || got.Parts[0].Content.Format != "" || got.Parts[0].Content.Body != "KakaoTalk post:\nFirst\n<literal> & second" {
		t.Fatal("plain source text interpreted as markup")
	}
}

func TestPostMalformedLayersAndUnknownFormsKeepGapIdentity(t *testing.T) {
	b, err := os.ReadFile("../../../research/fixtures/post/observed-text.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Attachment json.RawMessage `json:"attachment"`
	}
	if json.Unmarshal(b, &fixture) != nil {
		t.Fatal("fixture invalid")
	}
	base := string(fixture.Attachment)
	cases := map[string]string{
		"duplicate outer discriminator":    strings.Replace(base, `"t": 1`, `"t":1,"t":2`, 1),
		"duplicate nested text":            strings.Replace(base, `\"text\":\"Mooo-Synthetic-Post-Capture\"`, `\"text\":\"Mooo-Synthetic-Post-Capture\",\"text\":\"different\"`, 1),
		"unknown element":                  strings.Replace(base, `\"type\":\"text\"`, `\"type\":\"user\"`, 1),
		"oversized":                        strings.Repeat(" ", 64<<10) + base,
		"trailing":                         base + `{}`,
		"malformed structured no fallback": `{"os":[{"t":1,"ct":"must not hide error","jct":"not JSON"}]}`,
		"array carrier unverified":         `{"os":[{"t":1,"ct":"fallback","jct":[{"type":"text","text":"source"}]}]}`,
		"missing structured":               `{"os":[{"t":1,"ct":"legacy unobserved"}]}`,
		"null structured":                  `{"os":[{"t":1,"jct":null}]}`,
		"null element":                     `{"os":[{"t":1,"jct":"[null]"}]}`,
		"empty elements":                   `{"os":[{"t":1,"jct":"[]"}]}`,
		"unknown text attribute":           `{"os":[{"t":1,"jct":"[{\"type\":\"text\",\"text\":\"source\",\"style\":1}]"}]}`,
		"duplicate source object":          `{"os":[{"t":1,"jct":"[{\"type\":\"text\",\"text\":\"a\"}]"},{"t":1,"jct":"[{\"type\":\"text\",\"text\":\"b\"}]"}]}`,
	}
	for name, attachment := range cases {
		t.Run(name, func(t *testing.T) {
			if attachment == base {
				t.Fatal("mutation did not exercise guard")
			}
			if _, err := events.Decode(postPacket(t, attachment)); err == nil {
				t.Fatal("unsupported post accepted")
			}
			e, err := events.DecodeForDelivery(postPacket(t, attachment))
			if err != nil {
				t.Fatal(err)
			}
			gap, ok := e.(events.MessageGap)
			if !ok || gap.ChatID != testChatID || gap.LogID != 99 || gap.Type != messagetype.Post || gap.AuthorID != testOtherID {
				t.Fatal("post gap lost delivery identity")
			}
		})
	}
}

// Unknown post objects and unobserved link subtypes are counted and shown,
// not turned into a malformed-payload gap.
func TestPostUnknownObjectsAreCountedNotDropped(t *testing.T) {
	cases := map[string]string{
		"unknown object only":  `{"os":[{"t":99,"x":1}]}`,
		"text and unknown":     `{"os":[{"t":1,"jct":"[{\"type\":\"text\",\"text\":\"kept\"}]"},{"t":99}]}`,
		"unobserved link kind": `{"os":[{"t":1,"jct":"[{\"type\":\"text\",\"text\":\"kept\"}]"},{"t":2,"st":9,"url":"kakaomoim://x"}]}`,
	}
	for name, attachment := range cases {
		e, err := events.Decode(postPacket(t, attachment))
		post, ok := e.(events.PostMessage)
		if err != nil || !ok {
			t.Fatalf("%s: %T %v", name, e, err)
		}
		converted, err := convertPost(context.Background(), nil, nil, post)
		if err != nil || converted.Parts[0].Content.Body == "KakaoTalk post:" {
			t.Fatalf("%s: body %q", name, converted.Parts[0].Content.Body)
		}
	}
}
