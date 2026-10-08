package media

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func audioFixture(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("../../../research/fixtures/audio/observed-shape.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Attachment map[string]any `json:"attachment"`
	}
	if json.Unmarshal(b, &f) != nil {
		t.Fatal("fixture invalid")
	}
	return f.Attachment
}
func audioBody(t *testing.T, a map[string]any) []byte {
	t.Helper()
	x, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "authorId", Value: int64(8)}, {Key: "sendAt", Value: int64(1234)}, {Key: "type", Value: messagetype.Audio}, {Key: "attachment", Value: string(x)}}}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestObservedAudioMetadata(t *testing.T) {
	m, err := DecodeAudioMessage(audioBody(t, audioFixture(t)))
	if err != nil || m.ChatID != 42 || m.LogID != 99 || m.Attachment.Size != 110298 || m.Attachment.Duration != 51000 {
		t.Fatal("observed audio metadata lost", err)
	}
}
func TestAudioDownloadWithoutChecksumAndMillisecondExpiry(t *testing.T) {
	m, err := DecodeAudioMessage(audioBody(t, audioFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	// Synthetic ISO BMFF framing tests the framing guard, not codec parity.
	data := videoEnvelope()
	m.Attachment.Size = int64(len(data))
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	got, err := DownloadAudio(context.Background(), client, m.Attachment)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("no-checksum audio rejected", err)
	}
	m.Attachment.ExpiresAt = time.Now().UnixMilli() - 1
	if _, err = DownloadAudio(context.Background(), client, m.Attachment); err != ErrExpired {
		t.Fatal("expiry units lost", err)
	}
}
func TestAudioMalformedAttachments(t *testing.T) {
	for _, change := range []func(map[string]any){func(a map[string]any) { a["s"] = MaxAudioBytes + 1 }, func(a map[string]any) { a["d"] = -1 }, func(a map[string]any) { a["expire"] = 0 }, func(a map[string]any) { a["url"] = "https://example.com/audio" }, func(a map[string]any) { delete(a, "url") }} {
		a := audioFixture(t)
		change(a)
		if _, err := DecodeAudioMessage(audioBody(t, a)); err != ErrInvalidMessage {
			t.Fatal("invalid audio accepted")
		}
	}
}
