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

func contactMessage(t *testing.T) (events.ContactMessage, []byte) {
	t.Helper()
	b, err := os.ReadFile("../../../research/fixtures/contact/observed-shape.json")
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
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "authorId", Value: testOtherID}, {Key: "type", Value: messagetype.Contact}, {Key: "attachment", Value: string(f.Attachment)}}}})
	if err != nil {
		t.Fatal(err)
	}
	e, err := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := e.(events.ContactMessage)
	if !ok {
		t.Fatalf("contact untyped: %T", e)
	}
	chat, log, ok := events.MessagePosition(m)
	if !ok || chat != testChatID || log != 99 || m.Kind() != events.KindContactMessage {
		t.Fatal("contact identity lost")
	}
	return m, f.Data
}
func TestObservedContactEncryptedConversionPreservesBytesNameAndMIME(t *testing.T) {
	m, data := contactMessage(t)
	fileTransport(t, data)
	intent := &fileMatrixAPI{}
	got, err := convertContact(context.Background(), &bridgev2.Portal{Portal: &database.Portal{MXID: "!synthetic:localhost"}}, intent, m)
	if err != nil || len(got.Parts) != 1 {
		t.Fatal("contact conversion failed", err)
	}
	p := got.Parts[0]
	c := p.Content
	meta := p.DBMetadata.(*KakaoMessageMetadata)
	if c.MsgType != event.MsgFile || c.Body != "KakaoTalk contact: Mooo-Synthetic-Contact" || c.FileName != "contact.vcf" || c.File == nil || c.URL != "" || c.Info.MimeType != "text/vcard" || c.Info.Size != 115 || intent.name != "contact.vcf" || intent.mime != "text/vcard" || !bytes.Equal(intent.uploads[0], data) || meta.Type != messagetype.Contact {
		t.Fatal("native encrypted contact lost metadata/bytes")
	}
}
func TestContactTransientUploadFailureKeepsCursor(t *testing.T) {
	m, data := contactMessage(t)
	fileTransport(t, data)
	kc, _ := newTestClient(t, nil)
	fake := &fakeKakao{}
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		_, err := remote.(bridgev2.RemoteMessage).ConvertMessage(context.Background(), &bridgev2.Portal{Portal: &database.Portal{MXID: "!synthetic:localhost"}}, &fileMatrixAPI{albumMatrixAPI: albumMatrixAPI{failAt: 1}})
		if !errors.Is(err, errContactTransfer) {
			t.Fatal("upload failure not transient")
		}
		return bridgev2.EventHandlingResultFailed.WithError(err)
	}
	if kc.handleEvent(fake, m) || len(fake.committed()) != 0 {
		t.Fatal("failed contact committed cursor")
	}
}
func TestContactExpiredResourceDoesNotUpload(t *testing.T) {
	m, _ := contactMessage(t)
	m.Message.Attachment.ExpiresAt = 1
	intent := &fileMatrixAPI{}
	got, err := convertContact(context.Background(), nil, intent, m)
	if err != nil || got.Parts[0].Content.MsgType != event.MsgNotice || got.Parts[0].DBMetadata.(*KakaoMessageMetadata).ConversionGap != "expired" || len(intent.uploads) != 0 {
		t.Fatal("expired contact handling failed")
	}
}
