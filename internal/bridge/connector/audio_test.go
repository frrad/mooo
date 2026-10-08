package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func audioMessage(t *testing.T) (events.AudioMessage, []byte) {
	t.Helper()
	b, err := os.ReadFile("../../../research/fixtures/audio/observed-shape.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Attachment json.RawMessage `json:"attachment"`
		Data       []byte          `json:"data_base64"`
	}
	if json.Unmarshal(b, &f) != nil {
		t.Fatal("invalid fixture")
	}
	var attachment map[string]any
	if json.Unmarshal(f.Attachment, &attachment) != nil {
		t.Fatal("invalid attachment")
	}
	f.Data = []byte{0, 0, 0, 16, 'f', 't', 'y', 'p', '3', 'g', 'p', '4', 0, 0, 0, 0, 0, 0, 0, 9, 'm', 'o', 'o', 'v', 0, 0, 0, 0, 9, 'm', 'd', 'a', 't', 0}
	attachment["s"] = len(f.Data)
	f.Attachment, _ = json.Marshal(attachment)

	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "authorId", Value: testOtherID}, {Key: "type", Value: messagetype.Audio}, {Key: "attachment", Value: string(f.Attachment)}}}})
	if err != nil {
		t.Fatal(err)
	}
	e, err := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := e.(events.AudioMessage)
	if !ok {
		t.Fatalf("file untyped: %T", e)
	}
	chat, log, ok := events.MessagePosition(m)
	if !ok || chat != testChatID || log != 99 || m.Kind() != events.KindAudioMessage {
		t.Fatal("file identity lost")
	}
	return m, f.Data
}
func TestObservedAudioShapeConvertsSyntheticMediaWithMillisecondDuration(t *testing.T) {
	m, data := audioMessage(t)
	fileTransport(t, data)
	intent := &fileMatrixAPI{}
	got, err := convertAudio(context.Background(), &bridgev2.Portal{Portal: &database.Portal{MXID: "!synthetic:localhost"}}, intent, m)
	if err != nil || len(got.Parts) != 1 {
		t.Fatal("file conversion failed", err)
	}
	p := got.Parts[0]
	c := p.Content
	meta := p.DBMetadata.(*KakaoMessageMetadata)
	if c.MsgType != event.MsgAudio || c.Body != "audio.m4a" || c.FileName != c.Body || c.File == nil || c.URL != "" || c.Info.MimeType != "audio/mp4" || c.Info.Size != len(data) || c.Info.Duration != 51000 || intent.name != c.Body || intent.mime != "audio/mp4" || !bytes.Equal(intent.uploads[0], data) || meta.Type != messagetype.Audio {
		t.Fatal("native encrypted file lost metadata/bytes")
	}
}
func TestAudioTransientUploadFailureKeepsCursor(t *testing.T) {
	m, data := audioMessage(t)
	fileTransport(t, data)
	kc, _ := newTestClient(t, nil)
	fake := &fakeKakao{}
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		_, err := remote.(bridgev2.RemoteMessage).ConvertMessage(context.Background(), &bridgev2.Portal{Portal: &database.Portal{MXID: "!synthetic:localhost"}}, &fileMatrixAPI{albumMatrixAPI: albumMatrixAPI{failAt: 1}})
		if !errors.Is(err, errPhotoTransfer) {
			t.Fatal("upload failure not transient")
		}
		return bridgev2.EventHandlingResultFailed.WithError(err)
	}
	if kc.handleEvent(fake, m) || len(fake.committed()) != 0 {
		t.Fatal("failed file committed cursor")
	}
}
func TestAudioExpiredResourceDoesNotUpload(t *testing.T) {
	m, _ := audioMessage(t)
	m.Message.Attachment.ExpiresAt = 1
	intent := &fileMatrixAPI{}
	got, err := convertAudio(context.Background(), nil, intent, m)
	if err != nil || got.Parts[0].Content.MsgType != event.MsgNotice || got.Parts[0].DBMetadata.(*KakaoMessageMetadata).ConversionGap != "expired" || len(intent.uploads) != 0 {
		t.Fatal("expired file handling failed")
	}
}
