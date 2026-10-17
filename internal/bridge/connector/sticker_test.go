package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"net/http"
	"os"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/media"
	"maunium.net/go/mautrix/bridgev2"
	bridgedb "maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

func TestStickerIsConvertedInsteadOfUnsupported(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	remote := kc.remoteEventFor(events.StickerMessage{ChatID: 42, LogID: 99, AuthorID: 8, Type: 12, Attachment: media.StickerAttachment{Path: "synthetic.png"}})
	if remote == nil {
		t.Fatal("sticker must produce remote event")
	}
	data := connectorPNG(t)
	old := stickerHTTPClient
	stickerHTTPClient = &http.Client{Transport: photoRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
	})}
	t.Cleanup(func() { stickerHTTPClient = old })
	intent := &photoMatrixAPI{}
	portal := &bridgev2.Portal{Portal: &bridgedb.Portal{MXID: "!synthetic:localhost"}}
	converted, err := convertSticker(context.Background(), portal, intent, events.StickerMessage{ChatID: 42, LogID: 99, AuthorID: 8, Type: 12, Attachment: media.StickerAttachment{Path: "synthetic.png"}})
	if err != nil {
		t.Fatal(err)
	}
	part := converted.Parts[0]
	if part.Type != event.EventSticker || part.Content.Info.MimeType != "image/png" || !bytes.Equal(intent.uploaded, data) {
		t.Fatal("native sticker/asset lost")
	}
}

func TestStickerFailureCursorPolicy(t *testing.T) {
	for _, status := range []int{404, 500} {
		old := stickerHTTPClient
		stickerHTTPClient = &http.Client{Transport: photoRoundTripper(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("synthetic")), Request: r}, nil
		})}
		kc, _ := newTestClient(t, nil)
		fake := &fakeKakao{}
		msg := events.StickerMessage{ChatID: testChatID, LogID: 99, AuthorID: testOtherID, Type: 20, Attachment: media.StickerAttachment{Path: "synthetic.webp"}}
		kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
			typed := remote.(*simplevent.Message[events.StickerMessage])
			result, err := typed.ConvertMessage(context.Background(), nil, nil)
			if status == 404 {
				if err != nil || result.Parts[0].Content.MsgType != event.MsgNotice {
					t.Fatal("missing asset needs explicit notice")
				}
				return bridgev2.EventHandlingResultSuccess
			}
			if !errors.Is(err, errStickerTransfer) {
				t.Fatal("server failure should remain transient")
			}
			return bridgev2.EventHandlingResultFailed.WithError(err)
		}
		handled := kc.handleEvent(fake, msg)
		if status == 404 {
			if !handled || len(fake.committed()) != 1 {
				t.Fatal("notice did not commit")
			}
		} else if handled || len(fake.committed()) != 0 {
			t.Fatal("transient failure committed cursor")
		}
		stickerHTTPClient = old
	}
}

// stickerWithTextFixture decodes the observed sticker-with-text log through
// the production MSG decoder.
func stickerWithTextFixture(t *testing.T) events.StickerMessage {
	t.Helper()
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
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: bson.D{
		{Key: "logId", Value: int64(401)}, {Key: "authorId", Value: testOtherID}, {Key: "type", Value: fixture.Type},
		{Key: "message", Value: fixture.Message}, {Key: "attachment", Value: string(attachment)},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	sticker, ok := decoded.(events.StickerMessage)
	if !ok {
		t.Fatalf("decoded %T, want StickerMessage", decoded)
	}
	return sticker
}

func serveSticker(t *testing.T, status int, data []byte) {
	t.Helper()
	old := stickerHTTPClient
	stickerHTTPClient = &http.Client{Transport: photoRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
	})}
	t.Cleanup(func() { stickerHTTPClient = old })
}

// Regression (owned observation, 2026-10-16): a sticker sent with text reached
// Matrix as the sticker alone. Both official clients render the sticker
// followed by the text, so the bridge sends the sticker and then the text.
func TestObservedStickerWithTextBecomesStickerThenText(t *testing.T) {
	sticker := stickerWithTextFixture(t)
	data := connectorPNG(t)
	serveSticker(t, http.StatusOK, data)
	kc, _ := newTestClient(t, nil)
	remote := kc.remoteEventFor(sticker)
	message, ok := remote.(bridgev2.RemoteMessage)
	if !ok {
		t.Fatalf("remote event %T is not a message", remote)
	}
	intent := &photoMatrixAPI{}
	portal := &bridgev2.Portal{Portal: &bridgedb.Portal{MXID: "!synthetic:localhost"}}
	converted, err := message.ConvertMessage(context.Background(), portal, intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(converted.Parts) != 2 {
		t.Fatalf("got %d parts, want sticker and text", len(converted.Parts))
	}
	stickerPart, textPart := converted.Parts[0], converted.Parts[1]
	if stickerPart.ID != "" || stickerPart.Type != event.EventSticker || !bytes.Equal(intent.uploaded, data) {
		t.Fatalf("sticker part = %+v", stickerPart)
	}
	if textPart.ID != stickerTextPartID || textPart.Type != event.EventMessage || textPart.Content.MsgType != event.MsgText || textPart.Content.Body != "mooo-sticker-text-1" {
		t.Fatalf("text part = %+v", textPart)
	}
	textMetadata := textPart.DBMetadata.(*KakaoMessageMetadata)
	if textMetadata.LogID != 401 || textMetadata.Type != 12 || textMetadata.AuthorID != testOtherID {
		t.Fatalf("text part metadata = %+v", textMetadata)
	}
	expected, ok := remote.(expectedPartsMessage)
	if !ok {
		t.Fatal("sticker with text must declare both parts")
	}
	if got := expected.ExpectedPartIDs(); len(got) != 2 || got[0] != "" || got[1] != stickerTextPartID {
		t.Fatalf("expected part IDs = %q", got)
	}
}

// A sticker bridged before its text was kept (or a send that stopped after
// the sticker) has only the sticker row. Redelivery adds the missing text
// without repeating the sticker.
func TestStickerWithTextResumesMissingTextPart(t *testing.T) {
	sticker := stickerWithTextFixture(t)
	serveSticker(t, http.StatusOK, connectorPNG(t))
	kc, _ := newTestClient(t, nil)
	remote := kc.remoteEventFor(sticker)
	upsert, ok := remote.(bridgev2.RemoteMessageUpsert)
	if !ok || remote.GetType() != bridgev2.RemoteEventMessageUpsert {
		t.Fatalf("remote event %T must be an upsert", remote)
	}
	res, err := upsert.HandleExisting(context.Background(), nil, nil, []*bridgedb.Message{{PartID: ""}})
	if err != nil || !res.ContinueMessageHandling {
		t.Fatal("sticker without its text treated as complete")
	}
	intent := &photoMatrixAPI{}
	portal := &bridgev2.Portal{Portal: &bridgedb.Portal{MXID: "!synthetic:localhost"}}
	converted, err := upsert.ConvertMessage(context.Background(), portal, intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(converted.Parts) != 1 || converted.Parts[0].ID != stickerTextPartID || converted.Parts[0].Content.Body != "mooo-sticker-text-1" {
		t.Fatalf("resumed parts = %+v", converted.Parts)
	}
	if intent.uploaded != nil {
		t.Fatal("completed sticker uploaded again")
	}
	res, err = upsert.HandleExisting(context.Background(), nil, nil, []*bridgedb.Message{{PartID: ""}, {PartID: stickerTextPartID}})
	if err != nil || res.ContinueMessageHandling {
		t.Fatal("complete sticker with text replayed")
	}
}

func TestStickerWithTextKeepsTextWhenStickerIsUnavailable(t *testing.T) {
	sticker := stickerWithTextFixture(t)
	serveSticker(t, http.StatusNotFound, []byte("synthetic"))
	kc, _ := newTestClient(t, nil)
	message := kc.remoteEventFor(sticker).(bridgev2.RemoteMessage)
	converted, err := message.ConvertMessage(context.Background(), &bridgev2.Portal{Portal: &bridgedb.Portal{MXID: "!synthetic:localhost"}}, &photoMatrixAPI{})
	if err != nil {
		t.Fatal(err)
	}
	if len(converted.Parts) != 2 {
		t.Fatalf("got %d parts, want notice and text", len(converted.Parts))
	}
	if converted.Parts[0].Content.MsgType != event.MsgNotice || converted.Parts[0].DBMetadata.(*KakaoMessageMetadata).ConversionGap != "unavailable" {
		t.Fatalf("sticker gap part = %+v", converted.Parts[0])
	}
	if converted.Parts[1].Content.MsgType != event.MsgText || converted.Parts[1].Content.Body != "mooo-sticker-text-1" {
		t.Fatalf("text part = %+v", converted.Parts[1])
	}
}

func TestStickerWithoutTextStaysOnePart(t *testing.T) {
	serveSticker(t, http.StatusOK, connectorPNG(t))
	kc, _ := newTestClient(t, nil)
	remote := kc.remoteEventFor(events.StickerMessage{ChatID: testChatID, LogID: 402, AuthorID: testOtherID, Type: 12, Attachment: media.StickerAttachment{Path: "synthetic.png"}})
	if _, multipart := remote.(expectedPartsMessage); multipart {
		t.Fatal("sticker without text declared extra parts")
	}
	converted, err := remote.(bridgev2.RemoteMessage).ConvertMessage(context.Background(), &bridgev2.Portal{Portal: &bridgedb.Portal{MXID: "!synthetic:localhost"}}, &photoMatrixAPI{})
	if err != nil {
		t.Fatal(err)
	}
	if len(converted.Parts) != 1 || converted.Parts[0].Type != event.EventSticker {
		t.Fatalf("parts = %+v", converted.Parts)
	}
}

// Observed 2026-10-16: Android's reply to a sticker sent with text carries
// src_type 12 and the text as src_message. A Matrix reply to either bridged
// part must send the same.
func TestReplyToStickerWithTextQuotesItsText(t *testing.T) {
	sticker := stickerWithTextFixture(t)
	serveSticker(t, http.StatusOK, connectorPNG(t))
	kc, _ := newTestClient(t, nil)
	converted, err := kc.remoteEventFor(sticker).(bridgev2.RemoteMessage).ConvertMessage(context.Background(), &bridgev2.Portal{Portal: &bridgedb.Portal{MXID: "!synthetic:localhost"}}, &photoMatrixAPI{})
	if err != nil {
		t.Fatal(err)
	}
	portalKey := makePortalKey(testChatID, kc.login.ID)
	for _, part := range converted.Parts {
		row := &bridgedb.Message{ID: makeMessageID(testChatID, 401), PartID: part.ID, Room: portalKey, SenderID: makeUserID(testOtherID), Metadata: part.DBMetadata}
		target, err := replyTargetFor(row, portalKey)
		if err != nil {
			t.Fatalf("part %q: %v", part.ID, err)
		}
		if target.Type != 12 || target.Message != "mooo-sticker-text-1" || target.LogID != 401 || target.UserID != testOtherID {
			t.Fatalf("part %q reply target = %+v", part.ID, target)
		}
	}
}

// Regression (owned observation, 2026-10-16): a reply carrying a sticker and
// text has message = the text, attach_type 12 and no attach_only. The bridge
// sent only the text with a "(KakaoTalk sticker)" marker. B's official client
// renders the reply text and then the sticker; both relate to the source.
func TestObservedStickerTextReplyBecomesTextThenSticker(t *testing.T) {
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
	attachment, err := json.Marshal(fixture.Attachment)
	if err != nil {
		t.Fatal(err)
	}
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: bson.D{
		{Key: "logId", Value: int64(402)}, {Key: "authorId", Value: testOtherID}, {Key: "type", Value: fixture.Type},
		{Key: "message", Value: fixture.Message}, {Key: "attachment", Value: string(attachment)},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reply.(events.ReplyMessage); !ok {
		t.Fatalf("decoded %T, want ReplyMessage", reply)
	}
	serveSticker(t, http.StatusOK, connectorPNG(t))
	kc, intent := newReplyFramework(t)
	fake := &fakeKakao{}
	kc.client = fake
	if !kc.handleEvent(fake, source) || !kc.handleEvent(fake, reply) {
		t.Fatal("source or sticker text reply was not committed")
	}
	sent := intent.sentMessages()
	if len(sent) != 4 {
		t.Fatalf("Matrix events = %d, want sticker, text, reply text, reply sticker", len(sent))
	}
	sourceSticker, sourceText := sent[0].content.Parsed.(*event.MessageEventContent), sent[1].content.Parsed.(*event.MessageEventContent)
	if sent[0].typ != event.EventSticker || sent[1].typ != event.EventMessage || sourceText.Body != "mooo-sticker-text-1" || sourceSticker.RelatesTo != nil {
		t.Fatalf("source events = %+v / %+v", sent[0], sent[1])
	}
	replyText, replySticker := sent[2].content.Parsed.(*event.MessageEventContent), sent[3].content.Parsed.(*event.MessageEventContent)
	if sent[2].typ != event.EventMessage || replyText.MsgType != event.MsgText || replyText.Body != "mooo-sticker-text-reply-1" {
		t.Fatalf("reply text event = %+v", replyText)
	}
	if sent[3].typ != event.EventSticker || replySticker.Info == nil || replySticker.Info.MimeType != "image/png" {
		t.Fatalf("reply sticker event = %+v", replySticker)
	}
	for _, content := range []*event.MessageEventContent{replyText, replySticker} {
		if content.RelatesTo == nil || content.RelatesTo.GetReplyTo() == "" {
			t.Fatalf("reply part does not relate to its source: %+v", content)
		}
	}
	parts, err := kc.store.messageParts(context.Background(), kc.login.ID, makeMessageID(testChatID, 402))
	if err != nil || len(parts) != 2 {
		t.Fatalf("stored reply parts = %d, %v", len(parts), err)
	}
	for _, part := range parts {
		metadata := part.Metadata.(*KakaoMessageMetadata)
		if metadata.Type != 26 || metadata.Preview != "mooo-sticker-text-reply-1" {
			t.Fatalf("reply part %q metadata = %+v", part.PartID, metadata)
		}
	}
}

// A sticker text reply bridged before the sticker was kept has only its text
// row. Redelivery adds the sticker without repeating the text.
func TestStickerTextReplyResumesMissingStickerPart(t *testing.T) {
	serveSticker(t, http.StatusOK, connectorPNG(t))
	kc, _ := newTestClient(t, nil)
	reply := events.ReplyMessage{ChatID: testChatID, LogID: 403, AuthorID: testOtherID, SentAt: 1700000403, Message: "synthetic reply text",
		Source:     events.ReplySource{LogID: 401, UserID: testOtherID, Type: 12, Message: "mooo-sticker-text-1"},
		Attachment: events.ReplyAttachment{Type: 12, Sticker: &media.StickerAttachment{Path: "synthetic/emot_002.png"}}}
	upsert, ok := kc.remoteEventFor(reply).(bridgev2.RemoteMessageUpsert)
	if !ok {
		t.Fatal("sticker text reply must be an upsert")
	}
	res, err := upsert.HandleExisting(context.Background(), nil, nil, []*bridgedb.Message{{PartID: ""}})
	if err != nil || !res.ContinueMessageHandling {
		t.Fatal("reply text without its sticker treated as complete")
	}
	converted, err := upsert.ConvertMessage(context.Background(), &bridgev2.Portal{Portal: &bridgedb.Portal{MXID: "!synthetic:localhost"}}, &photoMatrixAPI{})
	if err != nil {
		t.Fatal(err)
	}
	if len(converted.Parts) != 1 || converted.Parts[0].ID != replyStickerPartID || converted.Parts[0].Type != event.EventSticker {
		t.Fatalf("resumed parts = %+v", converted.Parts)
	}
	res, err = upsert.HandleExisting(context.Background(), nil, nil, []*bridgedb.Message{{PartID: ""}, {PartID: replyStickerPartID}})
	if err != nil || res.ContinueMessageHandling {
		t.Fatal("complete sticker text reply replayed")
	}
}
