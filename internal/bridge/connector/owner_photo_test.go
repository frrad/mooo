package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/messagetype"
)

// Regression for #337: a photo the owner sent from their Android phone carries
// an empty cs. The Mac client models no checksum, so the photo must bridge as
// an image with the exact downloaded bytes instead of a malformed_payload gap.
func TestObservedOwnerPhotoWithEmptyChecksumBridgesAsImage(t *testing.T) {
	raw, err := os.ReadFile("../../../research/fixtures/photo/observed-owner-android-empty-checksum.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		ChatLogMessage string         `json:"chat_log_message"`
		Attachment     map[string]any `json:"attachment"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Attachment["cs"] != "" {
		t.Fatalf("fixture cs = %v, want the observed empty string", fixture.Attachment["cs"])
	}
	data := syntheticConnectorJPEG(t)
	fixture.Attachment["s"] = len(data)
	attachment, _ := json.Marshal(fixture.Attachment)
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "logId", Value: int64(160)}, {Key: "chatLog", Value: bson.D{
		{Key: "logId", Value: int64(160)}, {Key: "chatId", Value: testChatID}, {Key: "authorId", Value: testSelfID}, {Key: "type", Value: int32(messagetype.Photo)},
		{Key: "sendAt", Value: int32(1700000160)}, {Key: "message", Value: fixture.ChatLogMessage}, {Key: "attachment", Value: string(attachment)},
		{Key: "prevId", Value: int64(159)}, {Key: "msgId", Value: int64(1)}, {Key: "revision", Value: int32(0)}, {Key: "scope", Value: int32(1)},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	photo, ok := decoded.(events.PhotoMessage)
	if !ok {
		t.Fatalf("decoded %#v, want PhotoMessage", decoded)
	}
	oldClient := photoHTTPClient
	photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	t.Cleanup(func() { photoHTTPClient = oldClient })
	intent := &photoMatrixAPI{}
	converted, err := convertPhoto(context.Background(), &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:test"}}, intent, photo)
	if err != nil {
		t.Fatal(err)
	}
	content := converted.Parts[0].Content
	if content.MsgType != event.MsgImage || content.Info.MimeType != "image/jpeg" || !bytes.Equal(intent.uploaded, data) {
		t.Fatalf("msgtype=%q mime=%q exact=%v", content.MsgType, content.Info.MimeType, bytes.Equal(intent.uploaded, data))
	}
}
