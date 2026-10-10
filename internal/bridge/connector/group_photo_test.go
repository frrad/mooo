package connector

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mau.fi/util/dbutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
)

// photoTransferIntent records Matrix uploads for the real framework path. An
// optional upload barrier holds UploadMedia until its context ends.
type photoTransferIntent struct {
	*frameworkPersistenceIntent
	mu            sync.Mutex
	uploads       [][]byte
	blockUpload   bool
	uploadEntered chan struct{}
	uploadOnce    sync.Once
}

func (f *photoTransferIntent) UploadMedia(ctx context.Context, _ id.RoomID, data []byte, _, _ string) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	f.mu.Lock()
	block := f.blockUpload
	f.mu.Unlock()
	if block {
		f.uploadOnce.Do(func() { close(f.uploadEntered) })
		<-ctx.Done()
		return "", nil, ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads = append(f.uploads, append([]byte(nil), data...))
	return "mxc://example/photo", nil, nil
}

func (f *photoTransferIntent) uploaded() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.uploads...)
}

func groupPhotoEvent(t *testing.T, logID int64) (events.PhotoMessage, []byte) {
	t.Helper()
	data := connectorPNG(t)
	sum := sha1.Sum(data)
	return events.PhotoMessage{Message: media.PhotoMessage{
		ChatID: testChatID, LogID: logID, AuthorID: testOtherID, SentAt: 1700000100,
		Attachment: media.PhotoAttachment{
			Key: "synthetic", Width: 2, Height: 2, Size: int64(len(data)), Checksum: hex.EncodeToString(sum[:]),
			MediaType: "image/png", URL: "https://talk.kakaocdn.net/synthetic/photo", ThumbnailURL: "https://talk.kakaocdn.net/synthetic/thumb",
		},
	}}, data
}

// Disconnect must finish within its bound while an inbound photo transfer is
// in flight. The transfer is cancelled, the source position stays
// uncommitted, the profile owner is released, and the next connection replays
// the same photo exactly once.
func TestDisconnectCancelsInFlightGroupPhotoTransfer(t *testing.T) {
	for _, stage := range []string{"download", "upload"} {
		t.Run(stage, func(t *testing.T) {
			runDisconnectDuringPhotoTransfer(t, stage)
		})
	}
}

func runDisconnectDuringPhotoTransfer(t *testing.T, stage string) {
	ctx := context.Background()
	raw, err := dbutil.NewWithDialect(filepath.Join(t.TempDir(), "bridge.db"), "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.RawDB.Close() })
	photo, data := groupPhotoEvent(t, 110)
	downloadEntered := make(chan struct{})
	var downloadOnce sync.Once
	var transferMu sync.Mutex
	blockDownload := stage == "download"
	oldClient := photoHTTPClient
	photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
		transferMu.Lock()
		block := blockDownload
		transferMu.Unlock()
		if block {
			downloadOnce.Do(func() { close(downloadEntered) })
			<-req.Context().Done()
			return nil, req.Context().Err()
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	t.Cleanup(func() { photoHTTPClient = oldClient })
	intent := &photoTransferIntent{frameworkPersistenceIntent: &frameworkPersistenceIntent{}, blockUpload: stage == "upload", uploadEntered: make(chan struct{})}
	bridge, err := newFrameworkConversionBridge(ctx, raw, intent)
	if err != nil {
		t.Fatal(err)
	}
	user, err := bridge.GetUserByMXID(ctx, id.UserID("@owner:test"))
	if err != nil {
		t.Fatal(err)
	}
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{}, ID: makeUserLoginID(testSelfID), UserMXID: user.MXID}, Bridge: bridge, User: user}
	fake := &fakeKakao{stream: make(chan events.Result, 1)}
	opens := 0
	var openMu sync.Mutex
	kc := newKakaoClient(login, testSelfID, func() (kakaoClient, error) {
		openMu.Lock()
		defer openMu.Unlock()
		opens++
		return fake, nil
	})
	kc.sendState = func(status.BridgeState) {}
	kc.queue = login.QueueRemoteEvent
	t.Cleanup(kc.Disconnect)

	kc.Connect(ctx)
	if !kc.IsLoggedIn() {
		t.Fatal("client did not connect")
	}
	fake.stream <- events.Result{Event: photo}
	entered := downloadEntered
	if stage == "upload" {
		entered = intent.uploadEntered
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatalf("photo %s did not start", stage)
	}

	started := time.Now()
	kc.Disconnect()
	if elapsed := time.Since(started); elapsed >= terminalDisconnectTimeout {
		t.Fatalf("Disconnect during photo %s took %v, bound %v", stage, elapsed, terminalDisconnectTimeout)
	}
	kc.mu.Lock()
	retained := kc.cleanup
	kc.mu.Unlock()
	if retained != nil {
		t.Fatalf("Disconnect during photo %s retained the profile owner", stage)
	}
	if got := len(fake.committed()); got != 0 {
		t.Fatalf("cancelled photo %s committed %d source events", stage, got)
	}
	if intent.calls != 0 {
		t.Fatalf("cancelled photo %s sent %d Matrix events", stage, intent.calls)
	}

	// The next connection replays the uncommitted photo through catch-up.
	transferMu.Lock()
	blockDownload = false
	transferMu.Unlock()
	intent.mu.Lock()
	intent.blockUpload = false
	intent.mu.Unlock()
	fake.mu.Lock()
	fake.stream = make(chan events.Result)
	fake.resumeTargets = []syncmsg.Target{{ChatID: testChatID, MaxLogID: 110}}
	fake.catchUps = map[int64]catchUpResult{testChatID: {events: []events.Event{photo}}}
	fake.mu.Unlock()
	kc.Connect(ctx)
	if !kc.IsLoggedIn() {
		t.Fatal("client did not reconnect")
	}
	uploads := intent.uploaded()
	if len(uploads) != 1 || !bytes.Equal(uploads[0], data) {
		t.Fatalf("replayed uploads = %d, want one exact photo", len(uploads))
	}
	if intent.calls != 1 {
		t.Fatalf("replayed Matrix events = %d, want 1", intent.calls)
	}
	if got := fake.committed(); len(got) != 1 {
		t.Fatalf("replayed source commits = %d, want 1", len(got))
	}
	openMu.Lock()
	defer openMu.Unlock()
	if opens != 2 {
		t.Fatalf("profile opens = %d, want 2", opens)
	}
}

// A Kakao CDN refusal for a photo that is no longer available is final. The
// photo becomes a notice and its source position commits, instead of
// pausing delivery and replaying a download that cannot succeed.
func TestUnavailablePhotoDownloadBecomesCommittedNotice(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusGone} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			photo, _ := groupPhotoEvent(t, 120)
			oldClient := photoHTTPClient
			photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
			})}
			t.Cleanup(func() { photoHTTPClient = oldClient })
			kc, _ := newTestClient(t, nil)
			fake := &fakeKakao{}
			var notice *event.MessageEventContent
			var metadata *KakaoMessageMetadata
			kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
				msg := remote.(bridgev2.RemoteMessage)
				converted, err := msg.ConvertMessage(context.Background(), &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:test"}}, &photoMatrixAPI{})
				if err != nil {
					return bridgev2.EventHandlingResultFailed.WithError(err)
				}
				notice = converted.Parts[0].Content
				metadata, _ = converted.Parts[0].DBMetadata.(*KakaoMessageMetadata)
				return bridgev2.EventHandlingResultSuccess
			}
			if !kc.handleEvent(fake, photo) {
				t.Fatal("unavailable photo was left uncommitted for endless replay")
			}
			if len(fake.committed()) != 1 {
				t.Fatalf("source commits = %d, want 1", len(fake.committed()))
			}
			if notice == nil || notice.MsgType != event.MsgNotice || !strings.Contains(notice.Body, "unavailable") {
				t.Fatalf("notice = %+v", notice)
			}
			if metadata == nil || metadata.ConversionGap != "unavailable" || metadata.LogID != 120 || metadata.AuthorID != testOtherID {
				t.Fatalf("metadata = %+v", metadata)
			}
		})
	}
}

// Server errors and throttling stay retriable: the source position is kept.
func TestTransientPhotoHTTPStatusRemainsUncommitted(t *testing.T) {
	for _, code := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			photo, _ := groupPhotoEvent(t, 121)
			oldClient := photoHTTPClient
			photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
			})}
			t.Cleanup(func() { photoHTTPClient = oldClient })
			kc, _ := newTestClient(t, nil)
			fake := &fakeKakao{}
			kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
				msg := remote.(bridgev2.RemoteMessage)
				if _, err := msg.ConvertMessage(context.Background(), &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:test"}}, &photoMatrixAPI{}); err != nil {
					return bridgev2.EventHandlingResultFailed.WithError(err)
				}
				return bridgev2.EventHandlingResultSuccess
			}
			if kc.handleEvent(fake, photo) || len(fake.committed()) != 0 {
				t.Fatal("transient photo HTTP status advanced the source cursor")
			}
		})
	}
}

// A transient photo transfer failure retains the source position without
// posting the framework's generic error notice; replay would otherwise add
// one more notice to the room on every attempt.
func TestTransientPhotoFailurePostsNoMatrixErrorNotice(t *testing.T) {
	ctx := context.Background()
	raw, err := dbutil.NewWithDialect(filepath.Join(t.TempDir(), "bridge.db"), "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.RawDB.Close() })
	photo, data := groupPhotoEvent(t, 130)
	failing := true
	oldClient := photoHTTPClient
	photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
		if failing {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	t.Cleanup(func() { photoHTTPClient = oldClient })
	intent := &photoTransferIntent{frameworkPersistenceIntent: &frameworkPersistenceIntent{}, uploadEntered: make(chan struct{})}
	bridge, err := newFrameworkConversionBridge(ctx, raw, intent)
	if err != nil {
		t.Fatal(err)
	}
	user, err := bridge.GetUserByMXID(ctx, id.UserID("@owner:test"))
	if err != nil {
		t.Fatal(err)
	}
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{}, ID: makeUserLoginID(testSelfID), UserMXID: user.MXID}, Bridge: bridge, User: user}
	kc := newKakaoClient(login, testSelfID, nil)
	kc.sendState = func(status.BridgeState) {}
	fake := &fakeKakao{}
	for attempt := 0; attempt < 2; attempt++ {
		if kc.handleEvent(fake, photo) {
			t.Fatalf("attempt %d: transient photo failure was committed", attempt)
		}
	}
	if intent.calls != 0 || len(fake.committed()) != 0 {
		t.Fatalf("transient failures sent %d Matrix events and %d commits, want none", intent.calls, len(fake.committed()))
	}
	failing = false
	if !kc.handleEvent(fake, photo) {
		t.Fatal("replayed photo was not committed")
	}
	if intent.calls != 1 || len(fake.committed()) != 1 || len(intent.uploaded()) != 1 || !bytes.Equal(intent.uploaded()[0], data) {
		t.Fatalf("replay sent %d events, %d commits, %d uploads; want one exact photo", intent.calls, len(fake.committed()), len(intent.uploaded()))
	}
}

// The Matrix image is fetched and validated before the durable one-send
// reservation. A failed Matrix download or an image KakaoTalk cannot accept
// therefore leaves the event unreserved and never reaches the source.
func TestOutboundImageReservesOnlyAfterValidatedDownload(t *testing.T) {
	gif := []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")
	cases := []struct {
		name       string
		data       []byte
		downloadOK bool
		certain    bool
	}{
		{name: "matrix-download-failure", downloadOK: false, certain: true},
		{name: "unsupported-gif", data: gif, downloadOK: true, certain: true},
		{name: "not-an-image", data: []byte("not an image"), downloadOK: true, certain: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeKakao{}
			kc := connectedClient(t, fake)
			oldDownloader := matrixImageDownloader
			matrixImageDownloader = func(context.Context, bridgev2.MatrixAPI, id.ContentURIString, *event.EncryptedFileInfo) ([]byte, error) {
				if !tc.downloadOK {
					return nil, media.ErrInvalidImage
				}
				return append([]byte(nil), tc.data...), nil
			}
			t.Cleanup(func() { matrixImageDownloader = oldDownloader })
			reservations := 0
			_, err := kc.sendMatrixImage(context.Background(), fake, &photoMatrixAPI{}, testChatID, imageContent("mxc://example/image", nil), func(context.Context) error {
				reservations++
				return nil
			})
			if err == nil {
				t.Fatal("invalid outbound image was accepted")
			}
			if reservations != 0 || fake.imageCalls != 0 {
				t.Fatalf("reservations=%d source sends=%d, want none", reservations, fake.imageCalls)
			}
			var statusErr bridgev2.MessageStatus
			wantStatus := event.MessageStatusFail
			if !tc.downloadOK {
				// Nothing reached KakaoTalk, so resending is safe.
				wantStatus = event.MessageStatusRetriable
			}
			if !errors.As(err, &statusErr) || statusErr.IsCertain != tc.certain || statusErr.Status != wantStatus {
				t.Fatalf("status = %+v, want certain=%v %s", statusErr, tc.certain, wantStatus)
			}
		})
	}
}

func TestOutboundImageReservesOnceBeforeSingleSend(t *testing.T) {
	data := connectorPNG(t)
	chatLog, _ := bson.Marshal(bson.D{{Key: "logId", Value: int64(140)}, {Key: "sendAt", Value: int64(1700000140)}})
	fake := &fakeKakao{imageResp: media.SendResult{ChatLog: chatLog}}
	kc := connectedClient(t, fake)
	oldDownloader := matrixImageDownloader
	matrixImageDownloader = func(context.Context, bridgev2.MatrixAPI, id.ContentURIString, *event.EncryptedFileInfo) ([]byte, error) {
		return append([]byte(nil), data...), nil
	}
	t.Cleanup(func() { matrixImageDownloader = oldDownloader })
	reservations := 0
	resp, err := kc.sendMatrixImage(context.Background(), fake, &photoMatrixAPI{}, testChatID, imageContent("mxc://example/image", nil), func(context.Context) error {
		if fake.imageCalls != 0 {
			t.Fatal("reservation happened after the source send")
		}
		reservations++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if reservations != 1 || fake.imageCalls != 1 || !bytes.Equal(fake.imageData, data) || resp.DB.ID != makeMessageID(testChatID, 140) {
		t.Fatalf("reservations=%d sends=%d exact=%v id=%s", reservations, fake.imageCalls, bytes.Equal(fake.imageData, data), resp.DB.ID)
	}
}

// blockingImageKakao holds SendImage until its context ends, like an upload
// waiting for the media server's COMPLETE.
type blockingImageKakao struct {
	*fakeKakao
	entered chan struct{}
}

func (b *blockingImageKakao) SendImage(ctx context.Context, chatID int64, data []byte, caption string) (media.SendResult, error) {
	close(b.entered)
	<-ctx.Done()
	return media.SendResult{}, ctx.Err()
}

// Disconnect interrupts an outbound photo upload. The send is reported as
// unconfirmed (it may have reached KakaoTalk) and is never retried.
func TestDisconnectInterruptsOutboundPhotoUpload(t *testing.T) {
	fake := &fakeKakao{}
	kc := connectedClient(t, fake)
	blocking := &blockingImageKakao{fakeKakao: fake, entered: make(chan struct{})}
	oldDownloader := matrixImageDownloader
	matrixImageDownloader = func(context.Context, bridgev2.MatrixAPI, id.ContentURIString, *event.EncryptedFileInfo) ([]byte, error) {
		return connectorPNG(t), nil
	}
	t.Cleanup(func() { matrixImageDownloader = oldDownloader })
	type outcome struct {
		err error
		at  time.Time
	}
	result := make(chan outcome, 1)
	go func() {
		_, err := kc.sendMatrixImage(context.Background(), blocking, &photoMatrixAPI{}, testChatID, imageContent("mxc://example/image", nil), noReservation)
		result <- outcome{err: err, at: time.Now()}
	}()
	select {
	case <-blocking.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("outbound upload did not start")
	}
	// This fake's event stream outlives Shutdown, so Disconnect itself waits
	// for its bound here; the upload must end as soon as Disconnect starts.
	started := time.Now()
	disconnected := make(chan struct{})
	go func() {
		kc.Disconnect()
		close(disconnected)
	}()
	t.Cleanup(func() { <-disconnected })
	select {
	case got := <-result:
		if got.at.Sub(started) > time.Second {
			t.Fatalf("outbound upload ended %v after Disconnect", got.at.Sub(started))
		}
		var statusErr bridgev2.MessageStatus
		if !errors.As(got.err, &statusErr) || statusErr.IsCertain || statusErr.Status != event.MessageStatusFail {
			t.Fatalf("interrupted upload status = %+v, want unconfirmed failure", got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Disconnect did not interrupt the outbound upload")
	}
}

// observedPhotoEvent decodes the observed captioned group photo shape through
// the production MSG decoder, substituting synthetic bytes for the transfer.
func observedPhotoEvent(t *testing.T, withCaption bool) (events.PhotoMessage, []byte, string) {
	t.Helper()
	raw, err := os.ReadFile("../../../research/fixtures/photo/observed-group-caption.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		ChatLogMessage string         `json:"chat_log_message"`
		Attachment     map[string]any `json:"attachment"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	caption, _ := fixture.Attachment["cmt"].(string)
	if !withCaption {
		delete(fixture.Attachment, "cmt")
		caption = ""
	}
	data := connectorPNG(t)
	sum := sha1.Sum(data)
	fixture.Attachment["s"] = len(data)
	fixture.Attachment["cs"] = hex.EncodeToString(sum[:])
	fixture.Attachment["mt"] = "image/png"
	attachment, _ := json.Marshal(fixture.Attachment)
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: bson.D{
		{Key: "logId", Value: int64(150)}, {Key: "authorId", Value: testOtherID}, {Key: "type", Value: int32(media.PhotoType)},
		{Key: "message", Value: fixture.ChatLogMessage}, {Key: "attachment", Value: string(attachment)}, {Key: "sendAt", Value: int32(1700000150)},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	photo, ok := decoded.(events.PhotoMessage)
	if !ok {
		t.Fatalf("decoded %T, want PhotoMessage", decoded)
	}
	return photo, data, caption
}

// A Kakao photo caption (attachment cmt) becomes the Matrix image caption:
// body carries the caption while filename keeps the generated name.
func TestObservedGroupPhotoCaptionBecomesMatrixCaption(t *testing.T) {
	for _, withCaption := range []bool{true, false} {
		photo, data, caption := observedPhotoEvent(t, withCaption)
		oldClient := photoHTTPClient
		photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
		})}
		intent := &photoMatrixAPI{}
		converted, err := convertPhoto(context.Background(), &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:test"}}, intent, photo)
		photoHTTPClient = oldClient
		if err != nil {
			t.Fatal(err)
		}
		content := converted.Parts[0].Content
		wantBody := "photo.png"
		if withCaption {
			wantBody = caption
		}
		if content.MsgType != event.MsgImage || content.Body != wantBody || content.FileName != "photo.png" || !bytes.Equal(intent.uploaded, data) {
			t.Fatalf("caption=%v content body=%q filename=%q exact=%v", withCaption, content.Body, content.FileName, bytes.Equal(intent.uploaded, data))
		}
		if withCaption && caption == "" {
			t.Fatal("fixture lost its observed caption")
		}
	}
}

// Kakao labels JPEG photos "image/jpg"; Matrix receives the registered
// "image/jpeg" type.
func TestObservedGroupPhotoJPEGUsesRegisteredMIMEType(t *testing.T) {
	raw, err := os.ReadFile("../../../research/fixtures/photo/observed-group-caption.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Attachment map[string]any `json:"attachment"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Attachment["mt"] != "image/jpg" {
		t.Fatalf("fixture mt = %v, want observed image/jpg", fixture.Attachment["mt"])
	}
	data := syntheticConnectorJPEG(t)
	sum := sha1.Sum(data)
	fixture.Attachment["s"] = len(data)
	fixture.Attachment["cs"] = hex.EncodeToString(sum[:])
	attachment, _ := json.Marshal(fixture.Attachment)
	body, _ := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: bson.D{
		{Key: "logId", Value: int64(151)}, {Key: "authorId", Value: testOtherID}, {Key: "type", Value: int32(media.PhotoType)}, {Key: "attachment", Value: string(attachment)},
	}}})
	decoded, err := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
	if err != nil {
		t.Fatal(err)
	}
	oldClient := photoHTTPClient
	photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	t.Cleanup(func() { photoHTTPClient = oldClient })
	intent := &photoMatrixAPI{}
	converted, err := convertPhoto(context.Background(), &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:test"}}, intent, decoded.(events.PhotoMessage))
	if err != nil {
		t.Fatal(err)
	}
	content := converted.Parts[0].Content
	if content.Info.MimeType != "image/jpeg" || content.FileName != "photo.jpg" || !bytes.Equal(intent.uploaded, data) {
		t.Fatalf("mime=%q filename=%q exact=%v", content.Info.MimeType, content.FileName, bytes.Equal(intent.uploaded, data))
	}
}

func syntheticConnectorJPEG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := jpeg.Encode(&out, image.NewRGBA(image.Rect(0, 0, 4, 3)), nil); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestAlbumJPEGPartUsesRegisteredMIMEType(t *testing.T) {
	data := syntheticConnectorJPEG(t)
	sum := sha1.Sum(data)
	photo := media.PhotoAttachment{Key: "synthetic", Width: 4, Height: 3, Size: int64(len(data)), Checksum: hex.EncodeToString(sum[:]), MediaType: "image/jpg", URL: "https://talk.kakaocdn.net/synthetic/a", ThumbnailURL: "https://talk.kakaocdn.net/synthetic/t"}
	album := events.MultiPhotoMessage{Message: media.MultiPhotoMessage{ChatID: testChatID, LogID: 152, AuthorID: testOtherID, Photos: []media.PhotoAttachment{photo, photo}}}
	oldClient := photoHTTPClient
	photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	t.Cleanup(func() { photoHTTPClient = oldClient })
	converted, err := convertAlbum(context.Background(), &bridgev2.Portal{Portal: &database.Portal{MXID: "!room:test"}}, &photoMatrixAPI{}, album, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range converted.Parts {
		if part.Content.Info.MimeType != "image/jpeg" {
			t.Fatalf("album part mime = %q", part.Content.Info.MimeType)
		}
	}
}

func imageContent(uri id.ContentURIString, file *event.EncryptedFileInfo) *event.MessageEventContent {
	return &event.MessageEventContent{MsgType: event.MsgImage, Body: "image.png", URL: uri, File: file}
}

// A Matrix image caption (body differing from filename) is sent as the
// Kakao photo caption in the same single source send.
func TestOutboundImageCaptionIsSentWithPhoto(t *testing.T) {
	cases := []struct {
		name, body, filename, want string
	}{
		{name: "caption", body: "synthetic outbound caption", filename: "image.png", want: "synthetic outbound caption"},
		{name: "filename-only", body: "image.png", filename: "image.png", want: ""},
		{name: "legacy-body-is-filename", body: "image.png", filename: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chatLog, _ := bson.Marshal(bson.D{{Key: "logId", Value: int64(160)}, {Key: "sendAt", Value: int64(1700000160)}})
			fake := &fakeKakao{imageResp: media.SendResult{ChatLog: chatLog}}
			kc := connectedClient(t, fake)
			oldDownloader := matrixImageDownloader
			matrixImageDownloader = func(context.Context, bridgev2.MatrixAPI, id.ContentURIString, *event.EncryptedFileInfo) ([]byte, error) {
				return connectorPNG(t), nil
			}
			t.Cleanup(func() { matrixImageDownloader = oldDownloader })
			content := &event.MessageEventContent{MsgType: event.MsgImage, Body: tc.body, FileName: tc.filename, URL: "mxc://example/image"}
			if _, err := kc.sendMatrixImage(context.Background(), fake, &photoMatrixAPI{}, testChatID, content, noReservation); err != nil {
				t.Fatal(err)
			}
			if fake.imageCalls != 1 || fake.imageCaption != tc.want {
				t.Fatalf("sends=%d caption=%q, want one send with %q", fake.imageCalls, fake.imageCaption, tc.want)
			}
		})
	}
}

func TestOutboundImageOversizeCaptionRejectedBeforeReservation(t *testing.T) {
	fake := &fakeKakao{}
	kc := connectedClient(t, fake)
	oldDownloader := matrixImageDownloader
	matrixImageDownloader = func(context.Context, bridgev2.MatrixAPI, id.ContentURIString, *event.EncryptedFileInfo) ([]byte, error) {
		return connectorPNG(t), nil
	}
	t.Cleanup(func() { matrixImageDownloader = oldDownloader })
	content := &event.MessageEventContent{MsgType: event.MsgImage, Body: strings.Repeat("c", 16<<10+1), FileName: "image.png", URL: "mxc://example/image"}
	reservations := 0
	_, err := kc.sendMatrixImage(context.Background(), fake, &photoMatrixAPI{}, testChatID, content, func(context.Context) error {
		reservations++
		return nil
	})
	var statusErr bridgev2.MessageStatus
	if !errors.As(err, &statusErr) || !statusErr.IsCertain || reservations != 0 || fake.imageCalls != 0 {
		t.Fatalf("err=%v reservations=%d sends=%d", err, reservations, fake.imageCalls)
	}
}
