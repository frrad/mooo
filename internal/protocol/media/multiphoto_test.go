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
	got, err := DecodeMultiPhotoMessage(albumBody(t, albumFixture(t)))
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
		if _, err := DecodeMultiPhotoMessage(albumBody(t, a)); err != ErrInvalidMessage {
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
	if _, err = DecodeMultiPhotoMessage(body); err != ErrInvalidMessage {
		t.Fatal("duplicate array silently replaced")
	}
}

func TestMultiPhotoCommentsAreBoundedAndAligned(t *testing.T) {
	a := albumFixture(t)
	a["cmtl"] = []string{"synthetic caption", ""}
	got, err := DecodeMultiPhotoMessage(albumBody(t, a))
	if err != nil || got.Comments[0] != "synthetic caption" {
		t.Fatal("caption lost")
	}
	a["cmtl"] = []string{"only one"}
	if _, err := DecodeMultiPhotoMessage(albumBody(t, a)); err != ErrInvalidMessage {
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
	photo := PhotoAttachment{Width: 64, Height: 64, Size: int64(len(data)), Checksum: hex.EncodeToString(sum[:]), MediaType: "image/png", URL: "https://talk.kakaocdn.net/synthetic"}
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
	})}
	if _, err := DownloadAlbumPhoto(context.Background(), client, photo); err != ErrInvalidAttachment {
		t.Fatal("oversized actual canvas accepted")
	}
}
