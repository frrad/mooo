package connector

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
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
	"maunium.net/go/mautrix/bridgev2/simplevent"
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

func connectorPNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
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
	data := connectorPNG(t)
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

// encryptedPhotoMatrixAPI returns what bridgev2's ASIntent.UploadMedia returns
// for an encrypted room: an empty plain URL and an EncryptedFileInfo carrying
// the mxc URI (mautrix doUploadReq moves the URI into file.URL).
type encryptedPhotoMatrixAPI struct {
	bridgev2.MatrixAPI
	plainURL id.ContentURIString
}

func (f *encryptedPhotoMatrixAPI) UploadMedia(_ context.Context, _ id.RoomID, _ []byte, _, _ string) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	return f.plainURL, &event.EncryptedFileInfo{URL: "mxc://synthetic/encrypted-photo"}, nil
}

func encryptedRoomPhotoFixture(t *testing.T) events.PhotoMessage {
	t.Helper()
	data := connectorPNG(t)
	sum := sha1.Sum(data)
	oldClient := photoHTTPClient
	photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	t.Cleanup(func() { photoHTTPClient = oldClient })
	return events.PhotoMessage{Message: media.PhotoMessage{
		ChatID: testChatID, LogID: 81, AuthorID: testOtherID,
		Attachment: media.PhotoAttachment{Width: 2, Height: 2, Size: int64(len(data)), Checksum: hex.EncodeToString(sum[:]), MediaType: "image/png", URL: "https://talk.kakaocdn.net/photo"},
	}}
}

func assertEncryptedPhotoContent(t *testing.T, content *event.MessageEventContent) {
	t.Helper()
	if content.MsgType != event.MsgImage {
		t.Fatalf("msgtype = %q, want m.image", content.MsgType)
	}
	if content.File == nil || content.File.URL != "mxc://synthetic/encrypted-photo" {
		t.Fatalf("file = %+v, want encrypted file info", content.File)
	}
	if content.URL != "" {
		t.Fatalf("url = %q, want empty in encrypted room", content.URL)
	}
	raw, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["url"]; ok {
		t.Fatalf("encrypted photo content serialized a url field: %s", raw)
	}
	if _, ok := fields["file"]; !ok {
		t.Fatalf("encrypted photo content lacks a file field: %s", raw)
	}
}

// In an encrypted room the m.image content carries only `file`: the Matrix
// spec requires `url` for unencrypted media and `file` for encrypted media.
func TestConvertPhotoEncryptedRoomSendsFileWithoutURL(t *testing.T) {
	msg := encryptedRoomPhotoFixture(t)
	portal := &bridgev2.Portal{Portal: &bridgev2database.Portal{MXID: "!encrypted:test"}}
	converted, err := convertPhoto(context.Background(), portal, &encryptedPhotoMatrixAPI{plainURL: ""}, msg)
	if err != nil {
		t.Fatal(err)
	}
	assertEncryptedPhotoContent(t, converted.Parts[0].Content)
}

// A MatrixAPI that returns both a plain URL and encrypted file info violates
// the bridgev2 contract; photos drop the plain URL like every other attachment.
func TestConvertPhotoEncryptedRoomDropsPlainURLFromIntent(t *testing.T) {
	msg := encryptedRoomPhotoFixture(t)
	portal := &bridgev2.Portal{Portal: &bridgev2database.Portal{MXID: "!encrypted:test"}}
	converted, err := convertPhoto(context.Background(), portal, &encryptedPhotoMatrixAPI{plainURL: "mxc://synthetic/plain-photo"}, msg)
	if err != nil {
		t.Fatal(err)
	}
	assertEncryptedPhotoContent(t, converted.Parts[0].Content)
}

func TestConvertPhotoDeterministicFailureBecomesPersistableNotice(t *testing.T) {
	msg := events.PhotoMessage{Message: media.PhotoMessage{
		ChatID: testChatID, LogID: 77, AuthorID: testOtherID,
		Attachment: media.PhotoAttachment{Size: 1, Checksum: strings.Repeat("0", 40), MediaType: "image/jpeg", URL: "https://talk.kakaocdn.net/file", ExpiresAt: 1},
	}}
	converted, err := convertPhoto(context.Background(), nil, nil, msg)
	if err != nil {
		t.Fatal(err)
	}
	content := converted.Parts[0].Content
	metadata, ok := converted.Parts[0].DBMetadata.(*KakaoMessageMetadata)
	if content.MsgType != event.MsgNotice || !ok || metadata.ChatID != testChatID || metadata.LogID != 77 || metadata.AuthorID != testOtherID || metadata.Type != media.PhotoType || metadata.ConversionGap != "expired" {
		t.Fatalf("notice content=%+v metadata=%+v", content, metadata)
	}
	if strings.Contains(content.Body, "https://") || strings.Contains(content.Body, "file") {
		t.Fatalf("notice leaked source details: %q", content.Body)
	}
}

func TestDeterministicPhotoNoticeCommitsOriginalSourceEvent(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	fake := &fakeKakao{}
	photo := events.PhotoMessage{Message: media.PhotoMessage{
		ChatID: testChatID, LogID: 78, AuthorID: testOtherID,
		Attachment: media.PhotoAttachment{Size: 1, Checksum: strings.Repeat("0", 40), MediaType: "image/jpeg", URL: "https://talk.kakaocdn.net/file", ExpiresAt: 1},
	}}
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		msg, ok := remote.(*simplevent.Message[events.PhotoMessage])
		if !ok {
			t.Fatalf("queued event = %T", remote)
		}
		converted, err := msg.ConvertMessage(context.Background(), nil, nil)
		if err != nil || converted.Parts[0].Content.MsgType != event.MsgNotice {
			t.Fatalf("deterministic conversion = %#v/%v", converted, err)
		}
		return bridgev2.EventHandlingResultSuccess
	}
	if !kc.handleEvent(fake, photo) {
		t.Fatal("deterministic notice was not handled")
	}
	committed := fake.committed()
	if len(committed) != 1 || committed[0] != photo {
		t.Fatalf("committed source events = %#v, want one original event", committed)
	}
}

func TestDeterministicPhotoNoticeDeliveryFailureDoesNotCommit(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	fake := &fakeKakao{}
	photo := events.PhotoMessage{Message: media.PhotoMessage{ChatID: testChatID, LogID: 79, AuthorID: testOtherID, Attachment: media.PhotoAttachment{Size: 1, Checksum: strings.Repeat("0", 40), MediaType: "image/jpeg", URL: "https://talk.kakaocdn.net/file", ExpiresAt: 1}}}
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		msg := remote.(*simplevent.Message[events.PhotoMessage])
		if converted, err := msg.ConvertMessage(context.Background(), nil, nil); err != nil || converted.Parts[0].Content.MsgType != event.MsgNotice {
			t.Fatalf("notice conversion = %#v/%v", converted, err)
		}
		return bridgev2.EventHandlingResultFailed.WithError(errors.New("Matrix notice delivery failed"))
	}
	if kc.handleEvent(fake, photo) || len(fake.committed()) != 0 {
		t.Fatal("source event committed after notice delivery failure")
	}
}

func TestTransientPhotoFailuresRemainUncommitted(t *testing.T) {
	data := connectorPNG(t)
	sum := sha1.Sum(data)
	attachment := media.PhotoAttachment{Size: int64(len(data)), Checksum: hex.EncodeToString(sum[:]), MediaType: "image/png", URL: "https://talk.kakaocdn.net/photo"}
	photo := events.PhotoMessage{Message: media.PhotoMessage{ChatID: testChatID, LogID: 80, AuthorID: testOtherID, Attachment: attachment}}
	portal := &bridgev2.Portal{Portal: &bridgev2database.Portal{MXID: "!room:test"}}
	cases := []struct {
		name   string
		client *http.Client
		intent bridgev2.MatrixAPI
	}{
		{name: "network", client: &http.Client{Transport: photoRoundTripper(func(*http.Request) (*http.Response, error) { return nil, errors.New("temporary network failure") })}, intent: &photoMatrixAPI{}},
		{name: "truncated", client: &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data[:len(data)-1])), Request: req}, nil
		})}, intent: &photoMatrixAPI{}},
		{name: "upload", client: &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
		})}, intent: &photoMatrixAPI{uploadErr: errors.New("temporary Matrix upload failure")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldClient := photoHTTPClient
			photoHTTPClient = tc.client
			t.Cleanup(func() { photoHTTPClient = oldClient })
			kc, _ := newTestClient(t, nil)
			fake := &fakeKakao{}
			kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
				msg := remote.(*simplevent.Message[events.PhotoMessage])
				_, err := msg.ConvertMessage(context.Background(), portal, tc.intent)
				if err == nil {
					return bridgev2.EventHandlingResultSuccess
				}
				return bridgev2.EventHandlingResultFailed.WithError(err)
			}
			if kc.handleEvent(fake, photo) || len(fake.committed()) != 0 {
				t.Fatal("transient photo failure advanced source cursor")
			}
		})
	}
}

func TestQueuedPhotoHandlingDoesNotCommitSourceEvent(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	fake := &fakeKakao{}
	kc.queue = func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult { return bridgev2.EventHandlingResultQueued }
	photo := events.PhotoMessage{Message: media.PhotoMessage{ChatID: testChatID, LogID: 81, AuthorID: testOtherID, Attachment: media.PhotoAttachment{Size: 1, Checksum: strings.Repeat("0", 40), MediaType: "image/jpeg", URL: "https://talk.kakaocdn.net/file", ExpiresAt: 1}}}
	if kc.handleEvent(fake, photo) || len(fake.committed()) != 0 {
		t.Fatal("queued source event was committed")
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
	data, err := readBoundedMatrixMedia(reader, io.NopCloser(strings.NewReader("")), media.MaxImageBytes)
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
	data := connectorPNG(t)
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
	resp, err := kc.sendMatrixImage(context.Background(), fake, intent, 3000, imageContent("mxc://example/image", enc), noReservation)
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
	if _, err := kc.sendMatrixImage(context.Background(), fake, intent, 3000, imageContent("mxc://example/image", nil), noReservation); err == nil {
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
		return connectorPNG(t), nil
	}
	t.Cleanup(func() { matrixImageDownloader = oldDownloader })
	if _, err := kc.sendMatrixImage(context.Background(), fake, intent, 3000, imageContent("mxc://example/image", nil), noReservation); err == nil {
		t.Fatal("ambiguous send unexpectedly succeeded")
	}
	if fake.imageCalls != 1 || !bytes.Equal(fake.imageData, connectorPNG(t)) {
		t.Fatal("image was not attempted exactly once")
	}
}

func TestSendMatrixImageUsesCapturedClientAfterDisconnect(t *testing.T) {
	chatLog, _ := bson.Marshal(bson.D{{Key: "logId", Value: int64(78)}, {Key: "sendAt", Value: int64(1700000000)}})
	fake := &fakeKakao{imageResp: media.SendResult{ChatLog: chatLog}}
	kc := connectedClient(t, fake)
	oldDownloader := matrixImageDownloader
	matrixImageDownloader = func(context.Context, bridgev2.MatrixAPI, id.ContentURIString, *event.EncryptedFileInfo) ([]byte, error) {
		kc.mu.Lock()
		kc.client = nil
		kc.mu.Unlock()
		return connectorPNG(t), nil
	}
	t.Cleanup(func() { matrixImageDownloader = oldDownloader })
	if _, err := kc.sendMatrixImage(context.Background(), fake, &photoMatrixAPI{}, 3000, imageContent("mxc://example/image", nil), noReservation); err != nil {
		t.Fatal(err)
	}
	if fake.imageCalls != 1 {
		t.Fatalf("image calls=%d, want 1", fake.imageCalls)
	}
}

func noReservation(context.Context) error { return nil }
