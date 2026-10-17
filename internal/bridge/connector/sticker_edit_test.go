package connector

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
)

// decodeFixtureLog decodes one chat log through the production MSG decoder.
func decodeFixtureLog(t *testing.T, logID, authorID int64, typ int32, message string, attachment any) events.Event {
	t.Helper()
	log := bson.D{{Key: "logId", Value: logID}, {Key: "authorId", Value: authorID}, {Key: "type", Value: typ}, {Key: "message", Value: message}}
	if attachment != nil {
		raw, err := json.Marshal(attachment)
		if err != nil {
			t.Fatal(err)
		}
		log = append(log, bson.E{Key: "attachment", Value: string(raw)})
	}
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: log}})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

type stickerTextEditFixture struct {
	Feed struct {
		Type    int32          `json:"type"`
		Message map[string]any `json:"message"`
	} `json:"feed"`
	Edited struct {
		LogID      int64          `json:"logId"`
		Type       int32          `json:"type"`
		Message    string         `json:"message"`
		Attachment map[string]any `json:"attachment"`
	} `json:"edited"`
}

// stickerTextEdit decodes the observed edit of the sticker-with-text fixture
// (log 401): the feed-25 log as catch-up delivers it, completed with the
// edited log as GETMSGS returns it.
func stickerTextEdit(t *testing.T) events.MessageEdited {
	t.Helper()
	raw, err := os.ReadFile("../../../research/fixtures/stickers/observed-direct-sticker-text-edit.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture stickerTextEditFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	feedJSON, err := json.Marshal(fixture.Feed.Message)
	if err != nil {
		t.Fatal(err)
	}
	feed, ok := decodeFixtureLog(t, 403, testOtherID, fixture.Feed.Type, string(feedJSON), nil).(events.MessageEdited)
	if !ok {
		t.Fatal("feed did not decode to an edit")
	}
	edited := decodeFixtureLog(t, fixture.Edited.LogID, testOtherID, fixture.Edited.Type, fixture.Edited.Message, fixture.Edited.Attachment)
	if _, ok := edited.(events.StickerMessage); !ok {
		t.Fatalf("edited log decoded to %T", edited)
	}
	feed.Modified = edited
	return feed
}

// Regression (owned observation, 2026-10-16): Android and the Mac client let
// the author edit the text of a sticker sent with text; the feed-25 edit's
// message is the same type-12 log with the new text. The bridge ignored edits
// of non-text messages, so Matrix kept the old text. It now edits only the
// text part; the sticker is not re-sent, but its stored reply preview follows
// the new text, as the Mac reply summary does.
func TestObservedStickerTextEditEditsOnlyTheTextPart(t *testing.T) {
	source := stickerWithTextFixture(t)
	serveSticker(t, 200, connectorPNG(t))
	kc, intent := newReplyFramework(t)
	fake := &fakeKakao{}
	kc.client = fake
	if !kc.handleEvent(fake, source) {
		t.Fatal("sticker with text was not committed")
	}
	edit := stickerTextEdit(t)
	if edit.TargetLogID != source.LogID || edit.TargetRevision != 1 {
		t.Fatalf("edit = %+v", edit)
	}
	if !kc.handleEvent(fake, edit) {
		t.Fatal("edit was not committed")
	}
	sent := intent.sentMessages()
	if len(sent) != 3 {
		t.Fatalf("Matrix events = %d, want sticker, text and one text edit", len(sent))
	}
	parts, err := kc.store.messageParts(context.Background(), kc.login.ID, makeMessageID(testChatID, source.LogID))
	if err != nil || len(parts) != 2 {
		t.Fatalf("stored parts = %d, %v", len(parts), err)
	}
	byID := map[networkid.PartID]int{}
	for i, part := range parts {
		byID[part.PartID] = i
	}
	textPart := parts[byID[stickerTextPartID]]
	edited := sent[2].content.Parsed.(*event.MessageEventContent)
	if sent[2].typ != event.EventMessage || edited.RelatesTo == nil || edited.RelatesTo.Type != event.RelReplace || edited.RelatesTo.EventID != textPart.MXID {
		t.Fatalf("edit event = %+v, want a replacement of the text part %s", edited, textPart.MXID)
	}
	if edited.NewContent == nil || edited.NewContent.MsgType != event.MsgText || edited.NewContent.Body != "mooo-sticker-text-edit-1" {
		t.Fatalf("edited content = %+v", edited.NewContent)
	}
	for _, part := range parts {
		metadata := part.Metadata.(*KakaoMessageMetadata)
		if metadata.Revision != 1 || metadata.Preview != "mooo-sticker-text-edit-1" || metadata.Type != 12 {
			t.Fatalf("part %q metadata = %+v", part.PartID, metadata)
		}
	}
	// The same edit delivered again changes nothing.
	if !kc.handleEvent(fake, edit) || len(intent.sentMessages()) != 3 {
		t.Fatalf("replayed edit sent %d events", len(intent.sentMessages())-3)
	}
}

// The Mac client also allows editing a reply that carries a sticker and text
// (executed: availableForModify is true unless attach-only). The edit replaces
// the reply text and leaves the reply's sticker part alone.
func TestStickerTextReplyEditEditsOnlyTheTextPart(t *testing.T) {
	raw, err := os.ReadFile("../../../research/fixtures/replies/observed-direct-sticker-text-reply.json")
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
	source := stickerWithTextFixture(t)
	fixture.Attachment["src_logId"] = source.LogID
	fixture.Attachment["src_userId"] = source.AuthorID
	reply := decodeFixtureLog(t, 402, testOtherID, fixture.Type, fixture.Message, fixture.Attachment)
	modified := decodeFixtureLog(t, 402, testOtherID, fixture.Type, "mooo-sticker-text-reply-edit-1", fixture.Attachment)
	serveSticker(t, 200, connectorPNG(t))
	kc, intent := newReplyFramework(t)
	fake := &fakeKakao{}
	kc.client = fake
	if !kc.handleEvent(fake, source) || !kc.handleEvent(fake, reply) {
		t.Fatal("source or reply was not committed")
	}
	edit := events.MessageEdited{ChatID: testChatID, LogID: 404, AuthorID: testOtherID, TargetLogID: 402, TargetRevision: 1, Modified: modified}
	if !kc.handleEvent(fake, edit) {
		t.Fatal("edit was not committed")
	}
	sent := intent.sentMessages()
	if len(sent) != 5 {
		t.Fatalf("Matrix events = %d, want four messages and one text edit", len(sent))
	}
	parts, err := kc.store.messageParts(context.Background(), kc.login.ID, makeMessageID(testChatID, 402))
	if err != nil || len(parts) != 2 {
		t.Fatalf("stored reply parts = %d, %v", len(parts), err)
	}
	var textMXID string
	for _, part := range parts {
		if part.PartID == "" {
			textMXID = string(part.MXID)
		}
		metadata := part.Metadata.(*KakaoMessageMetadata)
		if metadata.Revision != 1 || metadata.Preview != "mooo-sticker-text-reply-edit-1" {
			t.Fatalf("reply part %q metadata = %+v", part.PartID, metadata)
		}
	}
	edited := sent[4].content.Parsed.(*event.MessageEventContent)
	if edited.RelatesTo == nil || edited.RelatesTo.Type != event.RelReplace || string(edited.RelatesTo.EventID) != textMXID || edited.NewContent == nil || edited.NewContent.Body != "mooo-sticker-text-reply-edit-1" {
		t.Fatalf("edit event = %+v", edited)
	}
}

// A sticker edit without text has nothing to show; it is ignored rather than
// blanking the text part.
func TestStickerEditWithoutTextIsIgnored(t *testing.T) {
	source := stickerWithTextFixture(t)
	serveSticker(t, 200, connectorPNG(t))
	kc, intent := newReplyFramework(t)
	fake := &fakeKakao{}
	kc.client = fake
	if !kc.handleEvent(fake, source) {
		t.Fatal("sticker with text was not committed")
	}
	modified := source
	modified.Message = ""
	edit := events.MessageEdited{ChatID: testChatID, LogID: 403, AuthorID: testOtherID, TargetLogID: source.LogID, TargetRevision: 1, Modified: modified}
	if !kc.handleEvent(fake, edit) || len(intent.sentMessages()) != 2 {
		t.Fatalf("Matrix events = %d, want no edit", len(intent.sentMessages()))
	}
}

// The Mac reply composer quotes a sticker with text by its text and a reply
// carrying a sticker by its reply text (executed fixture). mooo stores that
// summary as each bridged part's reply preview, which is what a Matrix reply
// sends as src_message.
func TestExecutedMacStickerReplySummariesMatchStoredPreviews(t *testing.T) {
	raw, err := os.ReadFile("../../../research/fixtures/stickers/executed-mac-sticker-text.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name  string `json:"name"`
			Input struct {
				Type       int32          `json:"type"`
				Message    string         `json:"message"`
				Attachment map[string]any `json:"attachment"`
			} `json:"input"`
			Expected struct {
				ReplySummary string `json:"reply_summary"`
			} `json:"expected"`
			MoooExpected  *string `json:"mooo_expected"`
			MoooDeviation string  `json:"mooo_deviation"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("no cases")
	}
	for i, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			want := c.Expected.ReplySummary
			if c.MoooExpected != nil {
				if c.MoooDeviation == "" {
					t.Fatal("mooo_expected without mooo_deviation")
				}
				want = *c.MoooExpected
			}
			logID := int64(500 + i)
			evt := decodeFixtureLog(t, logID, testOtherID, c.Input.Type, c.Input.Message, c.Input.Attachment)
			serveSticker(t, 200, connectorPNG(t))
			kc, _ := newReplyFramework(t)
			fake := &fakeKakao{}
			kc.client = fake
			if !kc.handleEvent(fake, evt) {
				t.Fatal("not committed")
			}
			parts, err := kc.store.messageParts(context.Background(), kc.login.ID, makeMessageID(testChatID, logID))
			if err != nil || len(parts) == 0 {
				t.Fatalf("stored parts = %d, %v", len(parts), err)
			}
			for _, part := range parts {
				if got := part.Metadata.(*KakaoMessageMetadata).Preview; got != want {
					t.Fatalf("part %q preview = %q, want %q", part.PartID, got, want)
				}
			}
		})
	}
}
