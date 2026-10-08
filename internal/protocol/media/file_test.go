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

func fileFixture(t *testing.T) (map[string]any, []byte) {
	t.Helper()
	b, err := os.ReadFile("../../../research/fixtures/file/observed-shape.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Attachment map[string]any `json:"attachment"`
		Data       []byte         `json:"data_base64"`
	}
	if json.Unmarshal(b, &f) != nil {
		t.Fatal("invalid fixture")
	}
	return f.Attachment, f.Data
}
func fileBody(t *testing.T, a map[string]any) []byte {
	t.Helper()
	x, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "authorId", Value: int64(8)}, {Key: "sendAt", Value: int64(1234)}, {Key: "type", Value: messagetype.File}, {Key: "attachment", Value: string(x)}}}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestObservedFilePreservesMetadataAndOriginalBytes(t *testing.T) {
	a, data := fileFixture(t)
	m, err := DecodeFileMessage(fileBody(t, a))
	if err != nil || m.ChatID != 42 || m.LogID != 99 || m.AuthorID != 8 || m.SentAt != 1234 || m.Attachment.Name != "mooo-file-synthetic.txt" || m.Attachment.Size != 48 {
		t.Fatal("observed file metadata lost", err)
	}
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	got, err := DownloadFile(context.Background(), client, m.Attachment)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("file bytes/checksum lost", err)
	}
}
func TestFileExpiryUsesMillisecondsAndStopsBeforeFetch(t *testing.T) {
	a, _ := fileFixture(t)
	m, err := DecodeFileMessage(fileBody(t, a))
	if err != nil {
		t.Fatal(err)
	}
	m.Attachment.ExpiresAt = time.Now().UnixMilli() - 1
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { t.Fatal("expired file fetched"); return nil, nil })}
	if _, err = DownloadFile(context.Background(), client, m.Attachment); err != ErrExpired {
		t.Fatal("millisecond expiry treated as seconds")
	}
}
func TestFileRejectsPathNamesAndMalformedMetadata(t *testing.T) {
	for _, change := range []func(map[string]any){
		func(a map[string]any) { a["name"] = "../private.txt" },
		func(a map[string]any) { a["name"] = "C:\\private.txt" },
		func(a map[string]any) { a["name"] = "bad\nname" },
		func(a map[string]any) { a["name"] = ".." },
		func(a map[string]any) { a["s"] = MaxFileBytes + 1 },
		func(a map[string]any) { a["expire"] = 0 },
		func(a map[string]any) { a["cs"] = "invalid" },
		func(a map[string]any) { a["url"] = "https://example.com/private" },
		func(a map[string]any) { delete(a, "url"); a["rsc"] = "synthetic-resource" },
	} {
		a, _ := fileFixture(t)
		change(a)
		if _, err := DecodeFileMessage(fileBody(t, a)); err != ErrInvalidMessage {
			t.Fatal("invalid file accepted")
		}
	}
}
func TestFileRejectsDuplicateJSONFields(t *testing.T) {
	a, _ := fileFixture(t)
	x, _ := json.Marshal(a)
	body, _ := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "type", Value: messagetype.File}, {Key: "attachment", Value: string(x[:len(x)-1]) + `,"s":1}`}}}})
	if _, err := DecodeFileMessage(body); err != ErrInvalidMessage {
		t.Fatal("duplicate file size accepted")
	}
}
func TestFileChecksumAndResponseBounds(t *testing.T) {
	a, data := fileFixture(t)
	m, err := DecodeFileMessage(fileBody(t, a))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	m.Attachment.Checksum = "0000000000000000000000000000000000000000"
	if _, err = DownloadFile(context.Background(), client, m.Attachment); err != ErrChecksumMismatch {
		t.Fatal("corrupt file accepted")
	}
	m.Attachment.Size = 47
	if _, err = DownloadFile(context.Background(), client, m.Attachment); err != ErrInvalidAttachment {
		t.Fatal("oversized response accepted")
	}
	m.Attachment.Size = 49
	if _, err = DownloadFile(context.Background(), client, m.Attachment); err != ErrDownload {
		t.Fatal("truncated response not transient")
	}
}
