package connector

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
)

// capturingIntent records the Matrix message content the framework sends.
type capturingIntent struct {
	*frameworkPersistenceIntent
	mu      sync.Mutex
	sent    []*event.MessageEventContent
	sticker []*event.MessageEventContent
}

func (c *capturingIntent) SendMessage(ctx context.Context, room id.RoomID, typ event.Type, content *event.Content, extra *bridgev2.MatrixSendExtra) (*mautrix.RespSendEvent, error) {
	c.mu.Lock()
	if parsed, ok := content.Parsed.(*event.MessageEventContent); ok {
		c.sent = append(c.sent, parsed)
		if typ == event.EventSticker {
			c.sticker = append(c.sticker, parsed)
		}
	}
	c.mu.Unlock()
	return c.frameworkPersistenceIntent.SendMessage(ctx, room, typ, content, extra)
}

func (c *capturingIntent) UploadMedia(_ context.Context, _ id.RoomID, _ []byte, _, _ string) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	return "mxc://example/sticker", nil, nil
}

func (c *capturingIntent) stickers() []*event.MessageEventContent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*event.MessageEventContent(nil), c.sticker...)
}

func newReplyFramework(t *testing.T) (*KakaoClient, *capturingIntent) {
	t.Helper()
	ctx := context.Background()
	raw, err := dbutil.NewWithDialect(filepath.Join(t.TempDir(), "bridge.db"), "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.RawDB.Close() })
	intent := &capturingIntent{frameworkPersistenceIntent: &frameworkPersistenceIntent{}}
	bridge, err := newFrameworkConversionBridge(ctx, raw, intent)
	if err != nil {
		t.Fatal(err)
	}
	user, err := bridge.GetUserByMXID(ctx, id.UserID("@owner:test"))
	if err != nil {
		t.Fatal(err)
	}
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{}, ID: makeUserLoginID(testSelfID), UserMXID: user.MXID}, Bridge: bridge, User: user}
	kc := newKakaoClient(login, testSelfID, nil)
	kc.sendState = func(status.BridgeState) {}
	return kc, intent
}

// A Kakao reply whose source is bridged becomes a Matrix reply without any
// quoted fallback text.
func TestInboundGroupReplyToBridgedSourceRelatesWithoutFallback(t *testing.T) {
	kc, intent := newReplyFramework(t)
	fake := &fakeKakao{}
	source := events.TextMessage{ChatID: testChatID, LogID: 200, AuthorID: testOtherID, SentAt: 1700000200, Message: "group source text"}
	reply := events.ReplyMessage{ChatID: testChatID, LogID: 201, AuthorID: testOtherID, SentAt: 1700000201, Message: "group reply text",
		Source: events.ReplySource{LogID: 200, UserID: testOtherID, Type: chat.TextType, Message: "group source text"}}
	if !kc.handleEvent(fake, source) || !kc.handleEvent(fake, reply) {
		t.Fatal("source or reply was not committed")
	}
	if len(intent.sent) != 2 {
		t.Fatalf("Matrix events = %d, want 2", len(intent.sent))
	}
	got := intent.sent[1]
	if got.Body != "group reply text" || got.RelatesTo == nil || got.RelatesTo.GetReplyTo() == "" {
		t.Fatalf("reply content body=%q relates=%+v", got.Body, got.RelatesTo)
	}
}

// When the Kakao reply's source was never bridged, the framework drops the
// relation. The reply then carries the source preview Kakao embedded in the
// reply as a quote, so its context is not silently lost.
func TestInboundGroupReplyToMissingSourceQuotesEmbeddedPreview(t *testing.T) {
	kc, intent := newReplyFramework(t)
	fake := &fakeKakao{}
	reply := events.ReplyMessage{ChatID: testChatID, LogID: 211, AuthorID: testOtherID, SentAt: 1700000211, Message: "reply to an unbridged source",
		Source: events.ReplySource{LogID: 150, UserID: testOtherID, Type: chat.TextType, Message: "older <source> line one\nline two"}}
	if !kc.handleEvent(fake, reply) {
		t.Fatal("reply was not committed")
	}
	if len(intent.sent) != 1 {
		t.Fatalf("Matrix events = %d, want 1", len(intent.sent))
	}
	got := intent.sent[0]
	if got.RelatesTo != nil && got.RelatesTo.GetReplyTo() != "" {
		t.Fatalf("missing source produced a Matrix relation to %s", got.RelatesTo.GetReplyTo())
	}
	wantBody := "> older <source> line one\n> line two\n\nreply to an unbridged source"
	if got.Body != wantBody {
		t.Fatalf("body = %q, want %q", got.Body, wantBody)
	}
	if got.Format != event.FormatHTML || !strings.Contains(got.FormattedBody, "<blockquote>older &lt;source&gt; line one<br>line two</blockquote>") {
		t.Fatalf("formatted = %q", got.FormattedBody)
	}
}

// A Matrix reply whose target is not a bridged KakaoTalk message is refused
// before any source mutation with a certain, explained status.
func TestOutboundReplyToUnbridgedTargetHasClearStatus(t *testing.T) {
	cases := map[string]func(*bridgev2.MatrixMessage){
		"unknown matrix event": func(msg *bridgev2.MatrixMessage) {
			msg.Content.RelatesTo = (&event.RelatesTo{}).SetReplyTo("$missing")
		},
		"row without source metadata": func(msg *bridgev2.MatrixMessage) {
			msg.ReplyTo = &database.Message{ID: makeMessageID(testChatID, 11), Room: makePortalKey(testChatID, makeUserLoginID(testSelfID)), SenderID: makeUserID(testOtherID)}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &fakeKakao{sendResp: chat.WriteResponse{LogID: 37}}
			kc := connectedClient(t, fake)
			msg := matrixMessage(event.MsgText, "answer")
			setup(msg)
			_, err := kc.HandleMatrixMessage(context.Background(), msg)
			var statusErr bridgev2.MessageStatus
			if !errors.Is(err, errMissingReplyMetadata) || !errors.As(err, &statusErr) || !statusErr.IsCertain || statusErr.Status != event.MessageStatusFail || statusErr.ErrorReason != event.MessageStatusUnsupported || !statusErr.SendNotice || statusErr.Message == "" {
				t.Fatalf("status = %+v (err %v)", statusErr, err)
			}
			if len(fake.sends) != 0 || len(fake.replies) != 0 {
				t.Fatal("unbridged reply target reached KakaoTalk")
			}
		})
	}
}

// History import converts replies through the same path: a reply whose
// source is in the imported interval stays a plain reply, while one whose
// source predates the interval carries the embedded source preview as a quote.
func TestGroupHistoryRepliesMapInsideIntervalAndQuoteOlderSources(t *testing.T) {
	kc, source := newHistoryTest(t)
	p, err := kc.login.Bridge.GetPortalByMXID(t.Context(), "!selected:test")
	if err != nil {
		t.Fatal(err)
	}
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		//nolint:staticcheck // Use the actual consumer and durable message mapping.
		return p.Internal().HandleRemoteEvent(historyFrameworkContext(t.Context(), remote), kc.login, remote.GetType(), remote)
	}
	sourceText := events.TextMessage{ChatID: 5000, LogID: 101, AuthorID: 2000, Message: "history source", SentAt: 1700000000}
	inside := events.ReplyMessage{ChatID: 5000, LogID: 102, AuthorID: 4000, Message: "reply inside", SentAt: 1700000001,
		Source: events.ReplySource{LogID: 101, UserID: 2000, Type: chat.TextType, Message: "history source"}}
	older := events.ReplyMessage{ChatID: 5000, LogID: 103, AuthorID: 2000, Message: "reply to older", SentAt: 1700000002,
		Source: events.ReplySource{LogID: 50, UserID: 4000, Type: chat.TextType, Message: "before the interval"}}
	source.pages = []client.HistoryPage{{Events: []events.Event{sourceText, inside, older}, Next: 103, Complete: true}}
	if done, err := kc.BackfillGroup(t.Context(), "!selected:test", 100, 103, 10, false); err != nil || !done {
		t.Fatalf("history: %t %v", done, err)
	}
	matrix := kc.login.Bridge.Matrix.(*groupCreationMatrix)
	matrix.mu.Lock()
	defer matrix.mu.Unlock()
	want := []string{"history source", "reply inside", "> before the interval\n\nreply to older"}
	if len(matrix.messageBodies) != len(want) {
		t.Fatalf("history bodies = %q", matrix.messageBodies)
	}
	for i := range want {
		if matrix.messageBodies[i] != want[i] {
			t.Fatalf("history body %d = %q, want %q", i, matrix.messageBodies[i], want[i])
		}
	}
}

// A sticker-only Kakao reply becomes a Matrix sticker that relates to the
// reply source, instead of the literal "(Emoticons)" placeholder text.
func TestInboundStickerReplyBecomesStickerReply(t *testing.T) {
	kc, intent := newReplyFramework(t)
	fake := &fakeKakao{}
	data := connectorPNG(t)
	old := stickerHTTPClient
	stickerHTTPClient = &http.Client{Transport: photoRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
	})}
	t.Cleanup(func() { stickerHTTPClient = old })
	source := events.TextMessage{ChatID: testChatID, LogID: 220, AuthorID: testOtherID, SentAt: 1700000220, Message: "sticker reply source"}
	reply := events.ReplyMessage{ChatID: testChatID, LogID: 221, AuthorID: testOtherID, SentAt: 1700000221, Message: "(Emoticons)",
		Source:     events.ReplySource{LogID: 220, UserID: testOtherID, Type: chat.TextType, Message: "sticker reply source"},
		Attachment: events.ReplyAttachment{Type: 12, Only: true, Sticker: &media.StickerAttachment{Path: "synthetic/emot_001.png"}}}
	if !kc.handleEvent(fake, source) || !kc.handleEvent(fake, reply) {
		t.Fatal("source or sticker reply was not committed")
	}
	stickers := intent.stickers()
	if len(stickers) != 1 {
		t.Fatalf("Matrix stickers = %d, want 1", len(stickers))
	}
	got := stickers[0]
	if got.RelatesTo == nil || got.RelatesTo.GetReplyTo() == "" || got.Info == nil || got.Info.MimeType != "image/png" || strings.Contains(got.Body, "(Emoticons)") {
		t.Fatalf("sticker reply content = %+v", got)
	}
}

// A reply attachment the bridge cannot render is an explicit notice that
// still relates to the source.
func TestInboundUnsupportedReplyAttachmentBecomesNotice(t *testing.T) {
	kc, intent := newReplyFramework(t)
	fake := &fakeKakao{}
	source := events.TextMessage{ChatID: testChatID, LogID: 230, AuthorID: testOtherID, SentAt: 1700000230, Message: "attachment source"}
	reply := events.ReplyMessage{ChatID: testChatID, LogID: 231, AuthorID: testOtherID, SentAt: 1700000231, Message: "",
		Source:     events.ReplySource{LogID: 230, UserID: testOtherID, Type: chat.TextType, Message: "attachment source"},
		Attachment: events.ReplyAttachment{Type: 71, Only: true}}
	if !kc.handleEvent(fake, source) || !kc.handleEvent(fake, reply) {
		t.Fatal("source or reply was not committed")
	}
	got := intent.sent[1]
	if got.MsgType != event.MsgNotice || !strings.Contains(got.Body, "type 71") || got.RelatesTo == nil || got.RelatesTo.GetReplyTo() == "" {
		t.Fatalf("unsupported attachment reply = %+v", got)
	}
}

// Android sends src_message "Photo" when replying to a photo. A Matrix
// reply to a bridged photo uses the same preview, also for rows stored
// with the older "[image]" preview.
func TestOutboundReplyToPhotoUsesPhotoPreview(t *testing.T) {
	for _, stored := range []string{"[image]", "Photo"} {
		fake := &fakeKakao{sendResp: chat.WriteResponse{LogID: 240}}
		kc := connectedClient(t, fake)
		msg := matrixMessage(event.MsgText, "reply to a photo")
		msg.ReplyTo = &database.Message{
			ID: makeMessageID(testChatID, 239), Room: makePortalKey(testChatID, makeUserLoginID(testSelfID)), SenderID: makeUserID(testOtherID),
			Metadata: newKakaoMessageMetadata(testChatID, 239, testOtherID, media.PhotoType, stored, 0),
		}
		if _, err := kc.HandleMatrixMessage(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
		if len(fake.replies) != 1 || fake.replies[0].request.Target.Type != media.PhotoType || fake.replies[0].request.Target.Message != "Photo" {
			t.Fatalf("stored %q: reply target = %+v", stored, fake.replies)
		}
	}
}

// KakaoTalk offers no photo attachment while replying, so a Matrix image
// sent as a reply is refused before any source mutation with a clear status.
func TestOutboundImageReplyRefusedWithClearStatus(t *testing.T) {
	fake := &fakeKakao{}
	kc := connectedClient(t, fake)
	msg := matrixMessage(event.MsgImage, "image.png")
	msg.Content.URL = "mxc://example/image"
	msg.ReplyTo = &database.Message{ID: makeMessageID(testChatID, 239), Room: makePortalKey(testChatID, makeUserLoginID(testSelfID)), SenderID: makeUserID(testOtherID),
		Metadata: newKakaoMessageMetadata(testChatID, 239, testOtherID, chat.TextType, "source", 0)}
	_, err := kc.HandleMatrixMessage(context.Background(), msg)
	var statusErr bridgev2.MessageStatus
	if !errors.Is(err, errUnsupportedImageReply) || !errors.As(err, &statusErr) || !statusErr.IsCertain || statusErr.ErrorReason != event.MessageStatusUnsupported || statusErr.Message == "" || !statusErr.SendNotice {
		t.Fatalf("image reply status = %+v (%v)", statusErr, err)
	}
	if fake.imageCalls != 0 || len(fake.replies) != 0 || len(fake.sends) != 0 {
		t.Fatal("image reply reached KakaoTalk")
	}
}
