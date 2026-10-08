package connector

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

func videoMessage(t *testing.T) (events.VideoMessage, []byte) {
	t.Helper()
	data, err := os.ReadFile("../../../research/fixtures/video/observed-shape.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Attachment map[string]any `json:"attachment"`
	}
	if json.Unmarshal(data, &f) != nil {
		t.Fatal("invalid fixture")
	}
	// Substitute synthetic box bytes for transfer tests; observed media stays private.
	clip := []byte{0, 0, 0, 20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0, 'i', 's', 'o', 'm', 0, 0, 0, 9, 'm', 'o', 'o', 'v', 1, 0, 0, 0, 9, 'm', 'd', 'a', 't', 2}
	sum := sha1.Sum(clip)
	f.Attachment["s"] = len(clip)
	f.Attachment["cs"] = hex.EncodeToString(sum[:])
	a, _ := json.Marshal(f.Attachment)
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "authorId", Value: testOtherID}, {Key: "type", Value: messagetype.Video}, {Key: "attachment", Value: string(a)}}}})
	if err != nil {
		t.Fatal(err)
	}
	e, err := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := e.(events.VideoMessage)
	if !ok {
		t.Fatalf("video untyped: %T", e)
	}
	chat, log, ok := events.MessagePosition(m)
	if !ok || chat != testChatID || log != 99 || m.Kind() != events.KindVideoMessage {
		t.Fatal("video identity lost")
	}
	return m, clip
}
func videoTransport(t *testing.T, data []byte) {
	t.Helper()
	old := photoHTTPClient
	photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	t.Cleanup(func() { photoHTTPClient = old })
}
func TestVideoEncryptedConversionAndCaption(t *testing.T) {
	m, data := videoMessage(t)
	videoTransport(t, data)
	intent := &albumMatrixAPI{}
	m.Message.Attachment.Comment = "synthetic caption"
	got, err := convertVideo(context.Background(), &bridgev2.Portal{Portal: &database.Portal{MXID: "!synthetic:localhost"}}, intent, m)
	if err != nil || len(got.Parts) != 1 {
		t.Fatal("video conversion failed", err)
	}
	p := got.Parts[0]
	c := p.Content
	meta := p.DBMetadata.(*KakaoMessageMetadata)
	if c.MsgType != event.MsgVideo || c.Body != "synthetic caption" || c.FileName != "video.mp4" || c.File == nil || c.URL != "" || c.Info.Duration != 3000 || c.Info.Width != 320 || c.Info.Height != 240 || c.Info.MimeType != "video/mp4" || c.Info.Size != len(data) || !bytes.Equal(intent.uploads[0], data) || meta.Type != messagetype.Video {
		t.Fatal("native encrypted video metadata/bytes lost")
	}
}
func TestVideoTransientUploadFailureKeepsCursor(t *testing.T) {
	m, data := videoMessage(t)
	videoTransport(t, data)
	kc, _ := newTestClient(t, nil)
	fake := &fakeKakao{}
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		_, err := remote.(bridgev2.RemoteMessage).ConvertMessage(context.Background(), &bridgev2.Portal{Portal: &database.Portal{MXID: "!synthetic:localhost"}}, &albumMatrixAPI{failAt: 1})
		if !errors.Is(err, errPhotoTransfer) {
			t.Fatal("upload failure not transient")
		}
		return bridgev2.EventHandlingResultFailed.WithError(err)
	}
	if kc.handleEvent(fake, m) || len(fake.committed()) != 0 {
		t.Fatal("failed video committed cursor")
	}
}
func TestVideoExpiredResourceBecomesNoticeWithoutUpload(t *testing.T) {
	m, _ := videoMessage(t)
	m.Message.Attachment.ExpiresAt = 1
	intent := &albumMatrixAPI{}
	got, err := convertVideo(context.Background(), nil, intent, m)
	if err != nil || got.Parts[0].Content.MsgType != event.MsgNotice || got.Parts[0].DBMetadata.(*KakaoMessageMetadata).ConversionGap != "expired" || len(intent.uploads) != 0 {
		t.Fatal("expired video handling failed")
	}
}
