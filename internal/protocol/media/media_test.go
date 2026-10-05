package media

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

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
	log, _ := bson.Marshal(bson.D{{Key: "type", Value: PhotoType}, {Key: "logId", Value: int64(99)}})
	body, _ := bson.Marshal(bson.D{{Key: "chatLog", Value: bson.Raw(log)}})
	result, err := DecodeComplete(body)
	if err != nil || result.ChatLog.Lookup("logId").Int64() != 99 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestDecodeAndDownloadPhotoMessage(t *testing.T) {
	data := syntheticJPEG(t)
	sum := sha1.Sum(data)
	checksum := strings.ToUpper(hex.EncodeToString(sum[:]))
	attachment := fmt.Sprintf(`{"k":"opaque","w":3,"h":2,"s":%d,"cs":"%s","mt":"image/jpg","url":"https://talk.kakaocdn.net/p/file?token=synthetic","thumbnailUrl":"https://talk.kakaocdn.net/p/thumb?token=synthetic","expire":4102444800}`, len(data), checksum)
	log, _ := bson.Marshal(bson.D{{Key: "type", Value: PhotoType}, {Key: "logId", Value: int64(99)}, {Key: "authorId", Value: int64(7)}, {Key: "sendAt", Value: int64(1234)}, {Key: "attachment", Value: attachment}})
	body, _ := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.Raw(log)}})
	message, err := DecodePhotoMessage(body)
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
