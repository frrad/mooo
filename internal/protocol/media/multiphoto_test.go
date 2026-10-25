package media

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/protocol/messagetype"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func albumFixture(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile("../../../research/fixtures/multiphoto/observed-shape.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Attachment map[string]any `json:"attachment"`
	}
	if json.Unmarshal(data, &f) != nil {
		t.Fatal("fixture invalid")
	}
	return f.Attachment
}
func albumBody(t *testing.T, a map[string]any) []byte {
	t.Helper()
	attachment, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "authorId", Value: int64(8)}, {Key: "sendAt", Value: int64(1234)}, {Key: "type", Value: messagetype.MultiPhoto}, {Key: "attachment", Value: string(attachment)}}}})
	if err != nil {
		t.Fatal(err)
	}
	return body
}
func TestObservedMultiPhotoPreservesOrderedArrays(t *testing.T) {
	got, err := DecodeMultiPhotoMessage(decodeEnvelope(t, albumBody(t, albumFixture(t))))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Photos) != 2 || got.ChatID != 42 || got.LogID != 99 || got.AuthorID != 8 || got.SentAt != 1234 {
		t.Fatal("album identity lost")
	}
	for i, p := range got.Photos {
		if p.Width != 64 || p.Height != 64 || p.MediaType != "image/png" || p.Size != []int64{139, 141}[i] {
			t.Fatalf("photo %d metadata lost", i)
		}
	}
	if got.Photos[0].URL == got.Photos[1].URL {
		t.Fatal("ordering/URLs collapsed")
	}
}

// Regression: a received album omitted csl entirely and became a delivery gap.
// Exercise the same shape using the existing synthetic image fixture.
func TestMultiPhotoAllowsAbsentAndEmptyChecksums(t *testing.T) {
	checksum := strings.Repeat("ab", 20)
	for _, tc := range []struct {
		name      string
		checksums any
		want      []string
	}{
		{name: "absent", want: []string{"", ""}},
		{name: "null", want: []string{"", ""}},
		{name: "empty entries", checksums: []string{"", ""}, want: []string{"", ""}},
		{name: "mixed", checksums: []string{checksum, ""}, want: []string{strings.ToUpper(checksum), ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := albumFixture(t)
			if tc.name == "absent" {
				delete(a, "csl")
			} else {
				a["csl"] = tc.checksums
			}
			got, err := DecodeMultiPhotoMessage(decodeEnvelope(t, albumBody(t, a)))
			if err != nil {
				t.Fatalf("album rejected: %v", err)
			}
			if len(got.Photos) != len(tc.want) {
				t.Fatalf("photos = %d, want %d", len(got.Photos), len(tc.want))
			}
			for i, want := range tc.want {
				if got.Photos[i].Checksum != want {
					t.Fatalf("photo %d checksum = %q, want %q", i, got.Photos[i].Checksum, want)
				}
			}
		})
	}
}

func TestMultiPhotoRejectsMalformedChecksumLists(t *testing.T) {
	for _, tc := range []struct {
		name      string
		checksums any
	}{
		{name: "empty list", checksums: []string{}},
		{name: "short list", checksums: []string{""}},
		{name: "long list", checksums: []string{"", "", ""}},
		{name: "invalid length", checksums: []string{"bad", ""}},
		{name: "invalid hex", checksums: []string{strings.Repeat("g", 40), ""}},
		{name: "wrong type", checksums: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := albumFixture(t)
			a["csl"] = tc.checksums
			if _, err := DecodeMultiPhotoMessage(decodeEnvelope(t, albumBody(t, a))); err != ErrInvalidMessage {
				t.Fatalf("decode error = %v, want ErrInvalidMessage", err)
			}
		})
	}
}

func TestMultiPhotoRejectsMisalignedAndUnsafeArrays(t *testing.T) {
	for _, mutate := range []func(map[string]any){
		func(a map[string]any) { a["wl"] = []int{64} },
		func(a map[string]any) {
			a["imageUrls"] = []string{"https://example.com/private", "https://talk.kakaocdn.net/synthetic"}
		},
		func(a map[string]any) { a["sl"] = []int64{MaxImageBytes + 1, 141} },
		func(a map[string]any) { a["csl"] = []string{"bad", "bad"} },
		func(a map[string]any) { a["hl"] = []int{4097, 64} },
		func(a map[string]any) { a["mtl"] = []string{"application/octet-stream", "image/png"} },
	} {
		a := albumFixture(t)
		mutate(a)
		if _, err := DecodeMultiPhotoMessage(decodeEnvelope(t, albumBody(t, a))); err != ErrInvalidMessage {
			t.Fatal("malformed album accepted")
		}
	}
}

func TestMultiPhotoDuplicateAttachmentFieldsAreRejected(t *testing.T) {
	body := albumBody(t, albumFixture(t))
	raw := bson.Raw(body)
	log := raw.Lookup("chatLog").Document()
	text := log.Lookup("attachment").StringValue()
	text = "{\"wl\":[1]," + text[1:]
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(99)}, {Key: "type", Value: messagetype.MultiPhoto}, {Key: "attachment", Value: text}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeMultiPhotoMessage(decodeEnvelope(t, body)); err != ErrInvalidMessage {
		t.Fatal("duplicate array silently replaced")
	}
}

func TestMultiPhotoCommentsAreBoundedAndAligned(t *testing.T) {
	a := albumFixture(t)
	a["cmtl"] = []string{"synthetic caption", ""}
	got, err := DecodeMultiPhotoMessage(decodeEnvelope(t, albumBody(t, a)))
	if err != nil || got.Comments[0] != "synthetic caption" {
		t.Fatal("caption lost")
	}
	a["cmtl"] = []string{"only one"}
	if _, err := DecodeMultiPhotoMessage(decodeEnvelope(t, albumBody(t, a))); err != ErrInvalidMessage {
		t.Fatal("misaligned captions accepted")
	}
}

func TestAlbumDownloadBoundsActualCanvas(t *testing.T) {
	var out bytes.Buffer
	if png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 4097, 1))) != nil {
		t.Fatal("PNG encoding failed")
	}
	data := out.Bytes()
	sum := sha1.Sum(data)
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
	})}
	for _, tc := range []struct{ name, checksum string }{
		{name: "with checksum", checksum: hex.EncodeToString(sum[:])},
		{name: "without checksum"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			photo := PhotoAttachment{Width: 64, Height: 64, Size: int64(len(data)), Checksum: tc.checksum, MediaType: "image/png", URL: "https://talk.kakaocdn.net/synthetic"}
			if _, err := DownloadAlbumPhoto(context.Background(), client, photo); err != ErrInvalidAttachment {
				t.Fatal("oversized actual canvas accepted")
			}
		})
	}
}
