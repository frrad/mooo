package media

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/chatlog"

	"github.com/frrad/mooo/internal/protocol/messagetype"
)

// decodeEnvelope decodes a synthetic MSG body the way events.Decode does
// before it hands the envelope to a media decoder.
func decodeEnvelope(t *testing.T, body []byte) chatlog.Envelope {
	t.Helper()
	envelope, err := chatlog.Decode(body)
	if err != nil {
		t.Fatalf("chatlog.Decode: %v", err)
	}
	return envelope
}

func syntheticJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Set(1, 1, color.RGBA{R: 0xff, A: 0xff})
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, nil); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPrepareImageAndShipRequest(t *testing.T) {
	prepared, err := PrepareImage(syntheticJPEG(t))
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Extension != "jpg" || prepared.Width != 3 || prepared.Height != 2 || len(prepared.Checksum) != 40 {
		t.Fatalf("unexpected prepared image: ext=%s dimensions=%dx%d checksum-len=%d", prepared.Extension, prepared.Width, prepared.Height, len(prepared.Checksum))
	}
	body, err := (ShipRequest{ChatID: 42, Image: prepared}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(body)
	for key, typ := range map[string]bson.Type{"c": bson.TypeInt64, "t": bson.TypeInt32, "s": bson.TypeInt64, "cs": bson.TypeString, "e": bson.TypeString, "ex": bson.TypeString} {
		if got := raw.Lookup(key).Type; got != typ {
			t.Fatalf("%s type = %v, want %v", key, got, typ)
		}
	}
}

func TestPrepareImageRejectsInvalidAndOversize(t *testing.T) {
	if _, err := PrepareImage([]byte("not an image")); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("invalid error = %v", err)
	}
	if _, err := PrepareImage(make([]byte, MaxImageBytes+1)); !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("oversize error = %v", err)
	}
}

func TestPostRequestAndResponseDecoders(t *testing.T) {
	prepared, err := PrepareImage(syntheticJPEG(t))
	if err != nil {
		t.Fatal(err)
	}
	body, err := (PostRequest{UserID: 7, Key: "ticket", ChatID: 42, Image: prepared, AppVersion: "26.8.0", MediaID: 9}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(body)
	for _, key := range []string{"u", "k", "t", "s", "c", "mid", "w", "h", "mm", "nt", "os", "av", "ex", "ns", "dt", "scp"} {
		if _, err := raw.LookupErr(key); err != nil {
			t.Fatalf("missing %s", key)
		}
	}
	shipBody, _ := bson.Marshal(bson.D{{Key: "k", Value: "ticket"}, {Key: "vh", Value: "media.invalid"}, {Key: "p", Value: int32(995)}})
	ship, err := DecodeShipResponse(shipBody)
	if err != nil || ship.Key != "ticket" || ship.Host != "media.invalid" || ship.Port != 995 {
		t.Fatalf("ship = %#v, err=%v", ship, err)
	}
	offsetBody, _ := bson.Marshal(bson.D{{Key: "o", Value: int64(len(prepared.Data))}})
	if offset, err := DecodePostOffset(offsetBody, len(prepared.Data)); err != nil || offset != len(prepared.Data) {
		t.Fatalf("offset=%d err=%v", offset, err)
	}
	badOffset, _ := bson.Marshal(bson.D{{Key: "o", Value: int64(len(prepared.Data) + 1)}})
	if _, err := DecodePostOffset(badOffset, len(prepared.Data)); !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("bad offset error = %v", err)
	}
}

func TestDecodeComplete(t *testing.T) {
	log, _ := bson.Marshal(bson.D{{Key: "type", Value: messagetype.Photo}, {Key: "logId", Value: int64(99)}})
	body, _ := bson.Marshal(bson.D{{Key: "chatLog", Value: bson.Raw(log)}})
	result, err := DecodeComplete(body)
	if err != nil || result.ChatLog.Lookup("logId").Int64() != 99 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

// Media decoders have always required chatLog.logId; the envelope's fallback
// to a top-level logId does not apply to them.
func TestMediaDecodersRejectTopLevelOnlyLogID(t *testing.T) {
	attachment := `{"k":"opaque","w":3,"h":2,"s":1,"cs":"0000000000000000000000000000000000000000","mt":"image/jpg","url":"https://talk.kakaocdn.net/p/file?token=synthetic","thumbnailUrl":"https://talk.kakaocdn.net/p/thumb?token=synthetic"}`
	withInner, _ := bson.Marshal(bson.D{
		{Key: "chatId", Value: int64(42)},
		{Key: "chatLog", Value: bson.D{{Key: "type", Value: messagetype.Photo}, {Key: "logId", Value: int64(99)}, {Key: "attachment", Value: attachment}}},
	})
	if _, err := DecodePhotoMessage(decodeEnvelope(t, withInner)); err != nil {
		t.Fatalf("photo with chatLog.logId: %v", err)
	}
	outerOnly, _ := bson.Marshal(bson.D{
		{Key: "chatId", Value: int64(42)},
		{Key: "logId", Value: int64(99)},
		{Key: "chatLog", Value: bson.D{{Key: "type", Value: messagetype.Photo}, {Key: "attachment", Value: attachment}}},
	})
	envelope := decodeEnvelope(t, outerOnly)
	if _, err := DecodePhotoMessage(envelope); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("photo with only top-level logId = %v; want ErrInvalidMessage", err)
	}
	for name, decode := range map[string]func() error{
		"audio": func() error {
			_, err := DecodeAudioMessage(withoutInnerLogID(t, audioBody(t, audioFixture(t))))
			return err
		},
		"contact": func() error {
			a, _ := contactFixture(t)
			_, err := DecodeContactMessage(withoutInnerLogID(t, contactBody(t, a)))
			return err
		},
		"file": func() error {
			a, _ := fileFixture(t)
			_, err := DecodeFileMessage(withoutInnerLogID(t, fileBody(t, a)))
			return err
		},
		"video": func() error {
			_, err := DecodeVideoMessage(withoutInnerLogID(t, videoBody(t, videoFixture(t))))
			return err
		},
		"multiphoto": func() error {
			_, err := DecodeMultiPhotoMessage(withoutInnerLogID(t, albumBody(t, albumFixture(t))))
			return err
		},
	} {
		if err := decode(); !errors.Is(err, ErrInvalidMessage) {
			t.Fatalf("%s decoder accepted a top-level-only logId: %v", name, err)
		}
	}
}

// withoutInnerLogID decodes a valid synthetic MSG body and marks its logId
// as coming from the top level, as chatlog.Decode does when chatLog.logId is
// absent.
func withoutInnerLogID(t *testing.T, body []byte) chatlog.Envelope {
	t.Helper()
	envelope := decodeEnvelope(t, body)
	envelope.LogIDInChatLog = false
	return envelope
}

func TestDecodeAndDownloadPhotoMessage(t *testing.T) {
	data := syntheticJPEG(t)
	sum := sha1.Sum(data)
	checksum := strings.ToUpper(hex.EncodeToString(sum[:]))
	attachment := fmt.Sprintf(`{"k":"opaque","w":3,"h":2,"s":%d,"cs":"%s","mt":"image/jpg","url":"https://talk.kakaocdn.net/p/file?token=synthetic","thumbnailUrl":"https://talk.kakaocdn.net/p/thumb?token=synthetic","expire":4102444800000}`, len(data), checksum)
	log, _ := bson.Marshal(bson.D{{Key: "type", Value: messagetype.Photo}, {Key: "logId", Value: int64(99)}, {Key: "authorId", Value: int64(7)}, {Key: "sendAt", Value: int64(1234)}, {Key: "attachment", Value: attachment}})
	body, _ := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.Raw(log)}})
	message, err := DecodePhotoMessage(decodeEnvelope(t, body))
	if err != nil || message.ChatID != 42 || message.LogID != 99 || message.AuthorID != 7 || message.SentAt != 1234 || message.Attachment.Width != 3 {
		t.Fatalf("message=%#v err=%v", message, err)
	}
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	downloaded, err := DownloadPhoto(t.Context(), client, message.Attachment)
	if err != nil || !bytes.Equal(downloaded, data) {
		t.Fatalf("download bytes=%d err=%v", len(downloaded), err)
	}
}

func TestPhotoDownloadRejectsExpiredAttachment(t *testing.T) {
	data := syntheticJPEG(t)
	sum := sha1.Sum(data)
	attachment := PhotoAttachment{Size: int64(len(data)), Checksum: hex.EncodeToString(sum[:]), MediaType: "image/jpeg", URL: "https://talk.kakaocdn.net/file", ExpiresAt: 1}
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("expired photo must not be requested")
		return nil, nil
	})}
	if _, err := DownloadPhoto(t.Context(), client, attachment); !errors.Is(err, ErrExpired) || !errors.Is(err, ErrDownload) {
		t.Fatalf("expired error = %v", err)
	}
}

func TestPhotoDownloadCancellationRemainsTransientBeforeExpiryClassification(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attachment := PhotoAttachment{Size: 1, Checksum: strings.Repeat("0", 40), URL: "https://talk.kakaocdn.net/file", ExpiresAt: 1}
	if _, err := DownloadPhoto(ctx, &http.Client{}, attachment); !errors.Is(err, ErrDownload) || errors.Is(err, ErrExpired) {
		t.Fatalf("canceled expired download error = %v", err)
	}
}

func TestPhotoDownloadClassifiesInvalidMediaMetadataAndBytes(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("not-an-image")), Request: req}, nil
	})}
	base := PhotoAttachment{Size: int64(len("not-an-image")), Checksum: strings.Repeat("0", 40), URL: "https://talk.kakaocdn.net/file"}
	if _, err := DownloadPhoto(t.Context(), client, base); !errors.Is(err, ErrInvalidAttachment) {
		t.Fatalf("missing media type error = %v", err)
	}
	base.MediaType = "image/png"
	if _, err := DownloadPhoto(t.Context(), client, base); !errors.Is(err, ErrUnsupportedImage) {
		t.Fatalf("unsupported bytes error = %v", err)
	}
}

func TestPhotoDownloadRejectsUnsafeURLAndBadChecksum(t *testing.T) {
	data := syntheticJPEG(t)
	attachment := PhotoAttachment{Size: int64(len(data)), Checksum: strings.Repeat("0", 40), MediaType: "image/jpeg", URL: "https://example.com/file?token=x"}
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, nil })}
	if _, err := DownloadPhoto(t.Context(), client, attachment); !errors.Is(err, ErrUnsafeURL) || !errors.Is(err, ErrDownload) {
		t.Fatalf("unsafe URL error = %v", err)
	}
	attachment.URL = "https://talk.kakaocdn.net/file?token=x"
	client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	if _, err := DownloadPhoto(t.Context(), client, attachment); !errors.Is(err, ErrChecksumMismatch) || !errors.Is(err, ErrDownload) {
		t.Fatalf("checksum error = %v", err)
	}
}

func TestPhotoDownloadRejectsUnsafeRedirectBeforeFollowing(t *testing.T) {
	data := syntheticJPEG(t)
	sum := sha1.Sum(data)
	requests := 0
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"https://example.com/private"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	})}
	attachment := PhotoAttachment{
		Size: int64(len(data)), Checksum: hex.EncodeToString(sum[:]), MediaType: "image/jpeg",
		URL: "https://talk.kakaocdn.net/file?sig=fixture",
	}
	if _, err := DownloadPhoto(t.Context(), client, attachment); !errors.Is(err, ErrDownload) {
		t.Fatalf("redirect error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, unsafe redirect was followed", requests)
	}
}

func TestDecodePhotoMessageCaptionBounds(t *testing.T) {
	encode := func(comment string) []byte {
		fields := map[string]any{"k": "opaque", "w": 3, "h": 2, "s": 10, "cs": strings.Repeat("A", 40), "mt": "image/jpeg",
			"url": "https://talk.kakaocdn.net/p/file", "thumbnailUrl": "https://talk.kakaocdn.net/p/thumb", "expire": 4102444800000, "cmt": comment}
		attachment, _ := json.Marshal(fields)
		log, _ := bson.Marshal(bson.D{{Key: "type", Value: messagetype.Photo}, {Key: "logId", Value: int64(99)}, {Key: "attachment", Value: string(attachment)}})
		body, _ := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.Raw(log)}})
		return body
	}
	message, err := DecodePhotoMessage(decodeEnvelope(t, encode("synthetic caption")))
	if err != nil || message.Attachment.Comment != "synthetic caption" {
		t.Fatalf("caption message=%q err=%v", message.Attachment.Comment, err)
	}
	for name, comment := range map[string]string{"oversize": strings.Repeat("c", 16<<10+1), "nul": "a\x00b"} {
		if _, err := DecodePhotoMessage(decodeEnvelope(t, encode(comment))); !errors.Is(err, ErrInvalidMessage) {
			t.Fatalf("%s caption error = %v", name, err)
		}
	}
}

func TestPhotoDownloadUnavailableStatusIsFinal(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusGone, http.StatusServiceUnavailable} {
		client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		})}
		_, err := DownloadPhoto(t.Context(), client, PhotoAttachment{Size: 10, MediaType: "image/jpeg", URL: "https://talk.kakaocdn.net/p/file", Checksum: strings.Repeat("A", 40)})
		if got, want := errors.Is(err, ErrUnavailable), code != http.StatusServiceUnavailable; got != want || !errors.Is(err, ErrDownload) {
			t.Fatalf("status %d: unavailable=%v err=%v", code, got, err)
		}
	}
}

func TestPostRequestCarriesCaptionInExtra(t *testing.T) {
	image, err := PrepareImage(syntheticJPEG(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ comment, want string }{{"", "{}"}, {"synthetic caption", `{"cmt":"synthetic caption"}`}} {
		body, err := (PostRequest{UserID: 7, Key: "k", ChatID: 42, Image: image, AppVersion: "26.8.0", MediaID: 1, Comment: tc.comment}).MarshalBSON()
		if err != nil {
			t.Fatal(err)
		}
		if got := bson.Raw(body).Lookup("ex").StringValue(); got != tc.want {
			t.Fatalf("POST ex = %q, want %q", got, tc.want)
		}
		ship, err := (ShipRequest{ChatID: 42, Image: image}).MarshalBSON()
		if err != nil || bson.Raw(ship).Lookup("ex").StringValue() != "{}" {
			t.Fatalf("SHIP ex changed: err=%v", err)
		}
	}
}

// Photo expire is epoch milliseconds (observed). A photo that expired an hour
// ago must not be requested, although the same number read as seconds would
// lie far in the future.
func TestPhotoExpiryUsesMilliseconds(t *testing.T) {
	requested := false
	client := &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		requested = true
		return nil, errors.New("unexpected request")
	})}
	expired := time.Now().Add(-time.Hour).UnixMilli()
	_, err := DownloadPhoto(t.Context(), client, PhotoAttachment{Size: 1, Checksum: strings.Repeat("0", 40), MediaType: "image/jpeg", URL: "https://talk.kakaocdn.net/file", ExpiresAt: expired})
	if !errors.Is(err, ErrExpired) || requested {
		t.Fatalf("expired-by-milliseconds photo err=%v requested=%v", err, requested)
	}
}

// Each rejected photo names its failed check, so a delivery gap can say why
// without retaining the attachment.
func TestDecodePhotoMessageReportsReason(t *testing.T) {
	encode := func(change func(map[string]any)) []byte {
		fields := map[string]any{"k": "opaque", "w": 3, "h": 2, "s": 10, "cs": strings.Repeat("A", 40), "mt": "image/jpeg",
			"url": "https://talk.kakaocdn.net/p/file", "thumbnailUrl": "https://talk.kakaocdn.net/p/thumb"}
		change(fields)
		attachment, _ := json.Marshal(fields)
		log, _ := bson.Marshal(bson.D{{Key: "type", Value: messagetype.Photo}, {Key: "logId", Value: int64(99)}, {Key: "attachment", Value: string(attachment)}})
		body, _ := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.Raw(log)}})
		return body
	}
	cases := map[string]func(map[string]any){
		"key":                   func(f map[string]any) { f["k"] = "" },
		"dimensions":            func(f map[string]any) { f["w"] = 0 },
		"size":                  func(f map[string]any) { f["s"] = MaxImageBytes + 1 },
		"checksum":              func(f map[string]any) { f["cs"] = strings.Repeat("Z", 40) },
		"media_type":            func(f map[string]any) { f["mt"] = "image/heic" },
		"attachment_json":       func(f map[string]any) { f["w"] = "3" },
		"url_host":              func(f map[string]any) { f["url"] = "https://elsewhere.example/p/file" },
		"url_scheme":            func(f map[string]any) { f["url"] = "http://talk.kakaocdn.net/p/file" },
		"url_authority":         func(f map[string]any) { f["url"] = "https://talk.kakaocdn.net:8443/p/file" },
		"url_path":              func(f map[string]any) { f["url"] = "https://talk.kakaocdn.net" },
		"thumbnail_url_missing": func(f map[string]any) { delete(f, "thumbnailUrl") },
		"caption":               func(f map[string]any) { f["cmt"] = "bad\x00caption" },
	}
	for reason, change := range cases {
		_, err := DecodePhotoMessage(decodeEnvelope(t, encode(change)))
		var invalid *InvalidMessageError
		if !errors.Is(err, ErrInvalidMessage) || !errors.As(err, &invalid) || invalid.Reason != reason {
			t.Errorf("%s: err = %v", reason, err)
		}
	}
}
