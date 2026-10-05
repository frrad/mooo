package connector

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/bridgev2"
	bridgev2database "maunium.net/go/mautrix/bridgev2/database"
	bridgematrix "maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/crypto/attachment"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
)

type photoMatrixAPI struct {
	bridgev2.MatrixAPI
	uploaded  []byte
	uploadErr error
}

type photoRoundTripper func(*http.Request) (*http.Response, error)

func (f photoRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func (f *photoMatrixAPI) UploadMedia(_ context.Context, _ id.RoomID, data []byte, _, _ string) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	f.uploaded = append([]byte(nil), data...)
	if f.uploadErr != nil {
		return "", nil, f.uploadErr
	}
	return "mxc://example/photo", nil, nil
}

func TestConvertPhotoSanitizesTransferErrors(t *testing.T) {
	oldClient := photoHTTPClient
	photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "GET", URL: "https://talk.kakaocdn.net/photo?secret=marker", Err: errors.New("marker")}
	})}
	t.Cleanup(func() { photoHTTPClient = oldClient })
	portal := &bridgev2.Portal{Portal: &bridgev2database.Portal{MXID: "!room:test"}}
	_, err := convertPhoto(context.Background(), portal, &photoMatrixAPI{}, events.PhotoMessage{Message: media.PhotoMessage{Attachment: media.PhotoAttachment{Size: 1, Width: 1, Height: 1, Checksum: strings.Repeat("0", 40), MediaType: "image/png", URL: "https://talk.kakaocdn.net/photo", ThumbnailURL: "https://talk.kakaocdn.net/thumb"}}})
	if err == nil || strings.Contains(err.Error(), "marker") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsanitized photo error: %v", err)
	}
}

func TestConvertPhotoDownloadsAndUploadsExactBytes(t *testing.T) {
	data := []byte("synthetic-photo-bytes")
	sum := sha1.Sum(data)
	attachment := media.PhotoAttachment{Width: 3, Height: 2, Size: int64(len(data)), Checksum: hex.EncodeToString(sum[:]), MediaType: "image/png", URL: "https://talk.kakaocdn.net/photo", ThumbnailURL: "https://talk.kakaocdn.net/thumb"}
	oldClient := photoHTTPClient
	photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	t.Cleanup(func() { photoHTTPClient = oldClient })
	intent := &photoMatrixAPI{}
	portal := &bridgev2.Portal{}
	portal.Portal = &bridgev2database.Portal{MXID: "!room:test"}
	converted, err := convertPhoto(context.Background(), portal, intent, events.PhotoMessage{Message: media.PhotoMessage{Attachment: attachment}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(intent.uploaded, data) {
		t.Fatalf("uploaded bytes = %q, want %q", intent.uploaded, data)
	}
	content := converted.Parts[0].Content
	if content.MsgType != event.MsgImage || content.FileName != "photo.png" || content.Info.Width != 3 || content.Info.Height != 2 {
		t.Fatalf("content = %+v", content)
	}
}

type countingReader struct {
	remaining int
	reads     int
}

func (r *countingReader) Read(dst []byte) (int, error) {
	r.reads += len(dst)
	for i := range dst {
		dst[i] = 'x'
	}
	r.remaining -= len(dst)
	if r.remaining <= 0 {
		return len(dst), io.EOF
	}
	return len(dst), nil
}

func TestReadBoundedMatrixImageStopsAtLimitPlusOne(t *testing.T) {
	reader := &countingReader{remaining: media.MaxImageBytes + 1024}
	data, err := readBoundedMatrixImage(reader, io.NopCloser(strings.NewReader("")))
	if err == nil || data != nil || reader.reads > media.MaxImageBytes+1 {
		t.Fatalf("bounded read data=%d err=%v read=%d", len(data), err, reader.reads)
	}
}

func TestDownloadMatrixImageBoundedEncryptedVector(t *testing.T) {
	plain := []byte("encrypted matrix photo")
	ciphertext := append([]byte(nil), plain...)
	file := &event.EncryptedFileInfo{EncryptedFile: *attachment.NewEncryptedFile()}
	file.EncryptInPlace(ciphertext)
	serialized, _ := json.Marshal(file)
	var decoded event.EncryptedFileInfo
	_ = json.Unmarshal(serialized, &decoded)
	file = &decoded
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(ciphertext) }))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	intent := &bridgematrix.ASIntent{Matrix: &appservice.IntentAPI{Client: &mautrix.Client{HomeserverURL: base, Client: server.Client()}}}
	file.URL = "mxc://example/media"
	got, err := downloadMatrixImageBounded(context.Background(), intent, file.URL, file)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("decrypted bytes=%q err=%v", got, err)
	}
	ciphertext[0] ^= 1
	if _, err = downloadMatrixImageBounded(context.Background(), intent, file.URL, file); err == nil {
		t.Fatal("tampered encrypted media unexpectedly accepted")
	}
}

func TestDownloadMatrixImageBoundedRejectsOversizeResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(media.MaxImageBytes+1))
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	intent := &bridgematrix.ASIntent{Matrix: &appservice.IntentAPI{Client: &mautrix.Client{HomeserverURL: base, Client: server.Client()}}}
	if _, err := downloadMatrixImageBounded(context.Background(), intent, "mxc://example/media", nil); err == nil {
		t.Fatal("oversize response unexpectedly accepted")
	}
}

func TestDownloadMatrixImageBoundedRejectsUnsupportedIntent(t *testing.T) {
	if _, err := downloadMatrixImageBounded(context.Background(), &photoMatrixAPI{}, "mxc://example/media", nil); err == nil {
		t.Fatal("unsupported Matrix intent unexpectedly accepted")
	}
}

func TestSendMatrixImageUsesEncryptedMediaAndSendsOnce(t *testing.T) {
	data := []byte("matrix-image")
	chatLog, _ := bson.Marshal(bson.D{{Key: "logId", Value: int64(77)}, {Key: "sendAt", Value: int64(1700000000)}})
	fake := &fakeKakao{imageResp: media.SendResult{ChatLog: chatLog}}
	kc := connectedClient(t, fake)
	intent := &photoMatrixAPI{}
	var seenFile *event.EncryptedFileInfo
	oldDownloader := matrixImageDownloader
	matrixImageDownloader = func(_ context.Context, _ bridgev2.MatrixAPI, _ id.ContentURIString, file *event.EncryptedFileInfo) ([]byte, error) {
		seenFile = file
		return append([]byte(nil), data...), nil
	}
	t.Cleanup(func() { matrixImageDownloader = oldDownloader })
	enc := &event.EncryptedFileInfo{}
	resp, err := kc.sendMatrixImage(context.Background(), fake, intent, 3000, "mxc://example/image", enc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fake.imageData, data) || resp.DB.ID != makeMessageID(3000, 77) || seenFile != enc {
		t.Fatalf("send data=%q response=%+v encrypted=%p", fake.imageData, resp, seenFile)
	}
}

func TestSendMatrixImageRejectsOversizeBeforeMutation(t *testing.T) {
	fake := &fakeKakao{imageResp: media.SendResult{}}
	kc := connectedClient(t, fake)
	intent := &photoMatrixAPI{}
	oldDownloader := matrixImageDownloader
	matrixImageDownloader = func(context.Context, bridgev2.MatrixAPI, id.ContentURIString, *event.EncryptedFileInfo) ([]byte, error) {
		return nil, media.ErrInvalidImage
	}
	t.Cleanup(func() { matrixImageDownloader = oldDownloader })
	if _, err := kc.sendMatrixImage(context.Background(), fake, intent, 3000, "mxc://example/image", nil); err == nil {
		t.Fatal("oversize image unexpectedly sent")
	}
	if fake.imageCalls != 0 {
		t.Fatalf("image mutation count data=%d", len(fake.imageData))
	}
}

func TestSendMatrixImageDoesNotRetryAmbiguousSend(t *testing.T) {
	fake := &fakeKakao{imageErr: errors.New("ambiguous")}
	kc := connectedClient(t, fake)
	intent := &photoMatrixAPI{}
	oldDownloader := matrixImageDownloader
	matrixImageDownloader = func(context.Context, bridgev2.MatrixAPI, id.ContentURIString, *event.EncryptedFileInfo) ([]byte, error) {
		return []byte("image"), nil
	}
	t.Cleanup(func() { matrixImageDownloader = oldDownloader })
	if _, err := kc.sendMatrixImage(context.Background(), fake, intent, 3000, "mxc://example/image", nil); err == nil {
		t.Fatal("ambiguous send unexpectedly succeeded")
	}
	if fake.imageCalls != 1 || !bytes.Equal(fake.imageData, []byte("image")) {
		t.Fatal("image was not attempted exactly once")
	}
}
