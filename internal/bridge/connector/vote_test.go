package connector

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/event"
)

func votePacket(t *testing.T, attachment string) loco.Packet {
	t.Helper()
	b, err := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "authorId", Value: testOtherID}, {Key: "type", Value: messagetype.Vote}, {Key: "attachment", Value: attachment}}}})
	if err != nil {
		t.Fatal(err)
	}
	return loco.Packet{Header: loco.Header{Method: "MSG"}, Body: b}
}

func TestObservedVoteCreationIsTyped(t *testing.T) {
	b, err := os.ReadFile("../../../research/fixtures/vote/observed-creation.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Attachment json.RawMessage `json:"attachment"`
		Native     struct {
			Title   string   `json:"title"`
			Options []string `json:"options"`
		} `json:"native_expected"`
		Expected struct {
			Body string `json:"body"`
		} `json:"mooo_expected"`
	}
	if err = json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	e, err := events.DecodeForDelivery(votePacket(t, string(fixture.Attachment)))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := e.(events.VoteMessage)
	if !ok {
		t.Fatalf("observed native poll lost as %T", e)
	}
	if m.Title != fixture.Native.Title || !reflect.DeepEqual(m.Options, fixture.Native.Options) {
		t.Fatal("native title/option order lost")
	}
	for _, positioned := range []events.Event{m, &m} {
		chat, log, ok := events.MessagePosition(positioned)
		if !ok || chat != testChatID || log != 99 {
			t.Fatal("poll delivery identity lost")
		}
	}
	got, err := convertVote(context.Background(), nil, nil, m)
	if err != nil || len(got.Parts) != 1 {
		t.Fatal("conversion failed", err)
	}
	c := got.Parts[0].Content
	if c.MsgType != event.MsgText || c.Body != fixture.Expected.Body || strings.Contains(c.Body, "kakaomoim") || got.Parts[0].DBMetadata.(*KakaoMessageMetadata).Type != messagetype.Vote {
		t.Fatal("poll snapshot or navigation boundary lost")
	}
}

func TestVoteMalformedOrUnverifiedFormsPreserveGapIdentity(t *testing.T) {
	b, err := os.ReadFile("../../../research/fixtures/vote/observed-creation.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Attachment json.RawMessage `json:"attachment"`
	}
	if json.Unmarshal(b, &f) != nil {
		t.Fatal("fixture invalid")
	}
	base := string(f.Attachment)
	cases := map[string]string{
		"duplicate root":     strings.Replace(base, `"subtype": 1`, `"subtype":1,"subtype":2`, 1),
		"duplicate object":   strings.Replace(base, `"st": 1`, `"st":1,"st":2`, 1),
		"duplicate option":   strings.Replace(base, `"tt": "Alpha"`, `"tt":"Alpha","tt":"Beta"`, 1),
		"closure unobserved": strings.Replace(base, `"subtype": 1`, `"subtype":4`, 1),
		"mismatched title":   strings.Replace(base, `"title": "Mooo-Synthetic-Poll"`, `"title":"Different"`, 1),
		"unknown object":     strings.Replace(base, `"t": 9`, `"t":99`, 1),
		"date options":       strings.Replace(base, `"t": 9`, `"t":9,"ittpe":"date"`, 1),
		"thumbnail":          strings.Replace(base, `"tt": "Alpha"`, `"tt":"Alpha","th":"https://example.com/photo"`, 1),
		"unknown option":     strings.Replace(base, `"tt": "Alpha"`, `"tt":"Alpha","unexpected":"data"`, 1),
		"null option":        strings.Replace(base, `{"tt": "Alpha"}`, `null`, 1),
		"missing vote ID":    strings.Replace(base, `"voteId": 0`, `"voteId":null`, 1),
		"bad vote ID":        strings.Replace(base, `"voteId": 0`, `"voteId":-1`, 1),
		"oversized":          strings.Repeat(" ", 64<<10) + base,
		"trailing value":     base + `{}`,
		"excessive depth":    strings.Replace(base, `"voteId": 0`, `"voteId":0,"extra":`+strings.Repeat("[", 18)+"0"+strings.Repeat("]", 18), 1),
	}
	for name, attachment := range cases {
		t.Run(name, func(t *testing.T) {
			if attachment == base {
				t.Fatal("mutation did not exercise intended guard")
			}
			if _, err := events.Decode(votePacket(t, attachment)); err == nil {
				t.Fatal("unverified poll accepted")
			}
			e, err := events.DecodeForDelivery(votePacket(t, attachment))
			if err != nil {
				t.Fatal(err)
			}
			gap, ok := e.(events.MessageGap)
			if !ok || gap.ChatID != testChatID || gap.LogID != 99 || gap.Type != messagetype.Vote || gap.AuthorID != testOtherID {
				t.Fatal("poll gap lost delivery identity")
			}
		})
	}
}
