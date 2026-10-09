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

func contactFixture(t *testing.T) (map[string]any, []byte) {
	t.Helper()
	b, err := os.ReadFile("../../../research/fixtures/contact/observed-shape.json")
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
func contactBody(t *testing.T, a map[string]any) []byte {
	t.Helper()
	x, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "authorId", Value: int64(8)}, {Key: "sendAt", Value: int64(1234)}, {Key: "type", Value: messagetype.Contact}, {Key: "attachment", Value: string(x)}}}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestObservedContactPreservesMetadataAndOriginalBytes(t *testing.T) {
	a, data := contactFixture(t)
	m, err := DecodeContactMessage(contactBody(t, a))
	if err != nil || m.ChatID != 42 || m.LogID != 99 || m.AuthorID != 8 || m.SentAt != 1234 || m.Attachment.Name != "Mooo-Synthetic-Contact" {
		t.Fatal("observed contact metadata lost", err)
	}
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	got, err := DownloadContact(context.Background(), client, m.Attachment)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("contact bytes/checksum lost", err)
	}
}
func TestContactExpiryUsesMillisecondsAndStopsBeforeFetch(t *testing.T) {
	a, _ := contactFixture(t)
	m, err := DecodeContactMessage(contactBody(t, a))
	if err != nil {
		t.Fatal(err)
	}
	m.Attachment.ExpiresAt = time.Now().UnixMilli() - 1
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { t.Fatal("expired contact fetched"); return nil, nil })}
	if _, err = DownloadContact(context.Background(), client, m.Attachment); err != ErrExpired {
		t.Fatal("millisecond expiry treated as seconds")
	}
}
func TestContactRejectsMalformedMetadata(t *testing.T) {
	for _, change := range []func(map[string]any){
		func(a map[string]any) { a["name"] = "bad\nname" },
		func(a map[string]any) { a["name"] = "" },
		func(a map[string]any) { a["expire"] = 0 },
		func(a map[string]any) { a["url"] = "https://example.com/private" },
		func(a map[string]any) { delete(a, "url"); a["rsc"] = "synthetic-resource" },
	} {
		a, _ := contactFixture(t)
		change(a)
		if _, err := DecodeContactMessage(contactBody(t, a)); err != ErrInvalidMessage {
			t.Fatal("invalid contact accepted")
		}
	}
}
func TestContactRejectsDuplicateJSONFields(t *testing.T) {
	a, _ := contactFixture(t)
	x, _ := json.Marshal(a)
	body, _ := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "type", Value: messagetype.Contact}, {Key: "attachment", Value: string(x[:len(x)-1]) + `,"name":"duplicate"}`}}}})
	if _, err := DecodeContactMessage(body); err != ErrInvalidMessage {
		t.Fatal("duplicate contact name accepted")
	}
}
func TestContactResponseBoundsAndFraming(t *testing.T) {
	a, data := contactFixture(t)
	m, err := DecodeContactMessage(contactBody(t, a))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{bytes.Repeat([]byte("x"), MaxContactBytes+1), []byte("not a vcard"), append(append([]byte{}, data...), data...)} {
		client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(bad)), Request: req}, nil
		})}
		if _, err = DownloadContact(context.Background(), client, m.Attachment); err != ErrInvalidAttachment {
			t.Fatal("invalid contact resource accepted", err)
		}
	}
}

func TestContactCRLFPolicyPreservesBytes(t *testing.T) {
	a, data := contactFixture(t)
	m, err := DecodeContactMessage(contactBody(t, a))
	if err != nil {
		t.Fatal(err)
	}
	crlf := bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(crlf)), Request: req}, nil
	})}
	got, err := DownloadContact(context.Background(), client, m.Attachment)
	if err != nil || !bytes.Equal(got, crlf) {
		t.Fatal("CRLF policy rewrote resource", err)
	}
}
