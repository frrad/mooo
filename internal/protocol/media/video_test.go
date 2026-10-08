package media

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func videoFixture(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("../../../research/fixtures/video/observed-shape.json")
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
func videoBody(t *testing.T, a map[string]any) []byte {
	t.Helper()
	x, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "authorId", Value: int64(8)}, {Key: "sendAt", Value: int64(1234)}, {Key: "type", Value: messagetype.Video}, {Key: "attachment", Value: string(x)}}}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestObservedVideoMetadata(t *testing.T) {
	m, err := DecodeVideoMessage(videoBody(t, videoFixture(t)))
	if err != nil || m.ChatID != 42 || m.LogID != 99 || m.AuthorID != 8 || m.SentAt != 1234 || m.Attachment.Size != 146274 || m.Attachment.Duration != 3 || m.Attachment.Width != 320 || m.Attachment.Height != 240 {
		t.Fatal("observed metadata lost", err)
	}
}
func TestVideoRejectsUnsafeAndUnboundedAttachments(t *testing.T) {
	for _, change := range []func(map[string]any){
		func(a map[string]any) { a["s"] = MaxVideoBytes + 1 },
		func(a map[string]any) { a["url"] = "https://example.com/private" },
		func(a map[string]any) { delete(a, "url"); a["rsc"] = "synthetic-resource" },
		func(a map[string]any) { a["cs"] = "invalid" },
		func(a map[string]any) { a["w"] = 8193 },
		func(a map[string]any) { a["d"] = -1 },
		func(a map[string]any) { a["cmt"] = string(make([]byte, 16385)) },
	} {
		a := videoFixture(t)
		change(a)
		if _, err := DecodeVideoMessage(videoBody(t, a)); err != ErrInvalidMessage {
			t.Fatal("invalid video accepted")
		}
	}
}
func TestVideoRejectsDuplicateFields(t *testing.T) {
	raw := bson.Raw(videoBody(t, videoFixture(t)))
	log := raw.Lookup("chatLog").Document()
	a := log.Lookup("attachment").StringValue()
	x, err := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "type", Value: messagetype.Video}, {Key: "attachment", Value: a[:len(a)-1] + `,"s":1}`}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeVideoMessage(x); err != ErrInvalidMessage {
		t.Fatal("duplicate field accepted")
	}
}

// Synthetic box framing only: this does not pretend to be an observed codec.
func videoEnvelope() []byte {
	return []byte{0, 0, 0, 20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0, 'i', 's', 'o', 'm', 0, 0, 0, 9, 'm', 'o', 'o', 'v', 1, 0, 0, 0, 9, 'm', 'd', 'a', 't', 2}
}
func TestVideoDownloadBoundsChecksumExpiryAndFraming(t *testing.T) {
	data := videoEnvelope()
	m, err := DecodeVideoMessage(videoBody(t, videoFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	a := m.Attachment
	a.Size = int64(len(data))
	sum := sha1.Sum(data)
	a.Checksum = hex.EncodeToString(sum[:])
	calls := 0
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	got, err := DownloadVideo(context.Background(), client, a)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("valid framing download failed", err)
	}
	a.ExpiresAt = 1
	if _, err = DownloadVideo(context.Background(), client, a); err != ErrExpired || calls != 1 {
		t.Fatal("expired video fetched")
	}
	a.ExpiresAt = 4102444800
	a.Checksum = "0000000000000000000000000000000000000000"
	if _, err = DownloadVideo(context.Background(), client, a); err != ErrChecksumMismatch {
		t.Fatal("checksum mismatch accepted")
	}
	a.Checksum = hex.EncodeToString(sum[:])
	a.Size--
	if _, err = DownloadVideo(context.Background(), client, a); err != ErrInvalidAttachment {
		t.Fatal("oversize response accepted")
	}
	a.Size += 2
	if _, err = DownloadVideo(context.Background(), client, a); err != ErrDownload {
		t.Fatal("short response not retryable")
	}
	for _, bad := range [][]byte{data[:len(data)-1], []byte("not a video"), append(bytes.Clone(data), 0), append([]byte{255, 255, 255, 255}, data[4:]...)} {
		if validMP4Envelope(bad) {
			t.Fatal("malformed framing accepted")
		}
	}
}
func TestVideoRedirectCannotLeaveAllowedOrigins(t *testing.T) {
	m, _ := DecodeVideoMessage(videoBody(t, videoFixture(t)))
	calls := 0
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://example.com/private"}}, Body: io.NopCloser(bytes.NewReader(nil)), Request: req}, nil
	})}
	if _, err := DownloadVideo(context.Background(), client, m.Attachment); err != ErrUnsafeURL || calls != 1 {
		t.Fatal("unsafe redirect followed")
	}
}
