package connector

import (
	"bytes"
	"context"
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
	"maunium.net/go/mautrix/id"
)

type fileMatrixAPI struct {
	albumMatrixAPI
	name, mime string
}

func (a *fileMatrixAPI) UploadMedia(ctx context.Context, room id.RoomID, data []byte, name, mime string) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	a.name, a.mime = name, mime
	return a.albumMatrixAPI.UploadMedia(ctx, room, data, name, mime)
}
func fileMessage(t *testing.T) (events.FileMessage, []byte) {
	t.Helper()
	b, err := os.ReadFile("../../../research/fixtures/file/observed-shape.json")
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
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "authorId", Value: testOtherID}, {Key: "type", Value: messagetype.File}, {Key: "attachment", Value: string(f.Attachment)}}}})
	if err != nil {
		t.Fatal(err)
	}
	e, err := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := e.(events.FileMessage)
	if !ok {
		t.Fatalf("file untyped: %T", e)
	}
	chat, log, ok := events.MessagePosition(m)
	if !ok || chat != testChatID || log != 99 || m.Kind() != events.KindFileMessage {
		t.Fatal("file identity lost")
	}
	return m, f.Data
}
func fileTransport(t *testing.T, data []byte) {
	t.Helper()
	old := photoHTTPClient
	photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	t.Cleanup(func() { photoHTTPClient = old })
}
func TestObservedFileEncryptedConversionPreservesBytesNameAndMIME(t *testing.T) {
	m, data := fileMessage(t)
	fileTransport(t, data)
	intent := &fileMatrixAPI{}
	got, err := convertFile(context.Background(), &bridgev2.Portal{Portal: &database.Portal{MXID: "!synthetic:localhost"}}, intent, m)
	if err != nil || len(got.Parts) != 1 {
		t.Fatal("file conversion failed", err)
	}
	p := got.Parts[0]
	c := p.Content
	meta := p.DBMetadata.(*KakaoMessageMetadata)
	if c.MsgType != event.MsgFile || c.Body != "mooo-file-synthetic.txt" || c.FileName != c.Body || c.File == nil || c.URL != "" || c.Info.MimeType != "text/plain" || c.Info.Size != 48 || intent.name != c.Body || intent.mime != "text/plain" || !bytes.Equal(intent.uploads[0], data) || meta.Type != messagetype.File {
		t.Fatal("native encrypted file lost metadata/bytes")
	}
}
func TestFileTransientUploadFailureKeepsCursor(t *testing.T) {
	m, data := fileMessage(t)
	fileTransport(t, data)
	kc, _ := newTestClient(t, nil)
	fake := &fakeKakao{}
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		_, err := remote.(bridgev2.RemoteMessage).ConvertMessage(context.Background(), &bridgev2.Portal{Portal: &database.Portal{MXID: "!synthetic:localhost"}}, &fileMatrixAPI{albumMatrixAPI: albumMatrixAPI{failAt: 1}})
		if !errors.Is(err, errFileTransfer) {
			t.Fatal("upload failure not transient")
		}
		return bridgev2.EventHandlingResultFailed.WithError(err)
	}
	if kc.handleEvent(fake, m) || len(fake.committed()) != 0 {
		t.Fatal("failed file committed cursor")
	}
}
func TestFileExpiredResourceDoesNotUpload(t *testing.T) {
	m, _ := fileMessage(t)
	m.Message.Attachment.ExpiresAt = 1
	intent := &fileMatrixAPI{}
	got, err := convertFile(context.Background(), nil, intent, m)
	if err != nil || got.Parts[0].Content.MsgType != event.MsgNotice || got.Parts[0].DBMetadata.(*KakaoMessageMetadata).ConversionGap != "expired" || len(intent.uploads) != 0 {
		t.Fatal("expired file handling failed")
	}
}
