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
)

// The observed group sticker shape decodes through the production MSG path
// and becomes a Matrix sticker with the downloaded resource.
func TestObservedGroupStickerShapeBecomesMatrixSticker(t *testing.T) {
	raw, err := os.ReadFile("../../../research/fixtures/stickers/observed-group-shape.json")
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
	attachment, _ := json.Marshal(fixture.Attachment)
	body, _ := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: bson.D{
		{Key: "logId", Value: int64(400)}, {Key: "authorId", Value: testOtherID}, {Key: "type", Value: fixture.Type},
		{Key: "message", Value: fixture.Message}, {Key: "attachment", Value: string(attachment)},
	}}})
	decoded, err := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	sticker, ok := decoded.(events.StickerMessage)
	if !ok || sticker.Attachment.Path != fixture.Attachment["path"] {
		t.Fatalf("decoded %#v", decoded)
	}
	data := connectorPNG(t)
	old := stickerHTTPClient
	stickerHTTPClient = &http.Client{Transport: photoRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
	})}
	t.Cleanup(func() { stickerHTTPClient = old })
	intent := &photoMatrixAPI{}
	converted, err := convertSticker(context.Background(), &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:test"}}, intent, sticker)
	if err != nil {
		t.Fatal(err)
	}
	part := converted.Parts[0]
	if part.Type != event.EventSticker || part.Content.Info.MimeType != "image/png" || !bytes.Equal(intent.uploaded, data) {
		t.Fatalf("sticker part = %+v", part)
	}
}
