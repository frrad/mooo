package connector

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/media"
)

func (f *fakeKakao) SendUpload(ctx context.Context, chatID int64, upload media.Upload) (media.SendResult, error) {
	f.uploads = append(f.uploads, upload)
	return f.uploadResp, f.uploadErr
}

func stubMatrixUploadDownload(t *testing.T, data []byte, err error) *int {
	t.Helper()
	downloads := 0
	old := matrixUploadDownloader
	matrixUploadDownloader = func(context.Context, bridgev2.MatrixAPI, id.ContentURIString, *event.EncryptedFileInfo) ([]byte, error) {
		downloads++
		if err != nil {
			return nil, err
		}
		return append([]byte(nil), data...), nil
	}
	t.Cleanup(func() { matrixUploadDownloader = old })
	return &downloads
}

func uploadChatLog(t *testing.T, logID int64) media.SendResult {
	t.Helper()
	raw, err := bson.Marshal(bson.D{{Key: "logId", Value: logID}, {Key: "sendAt", Value: int64(1700000150)}})
	if err != nil {
		t.Fatal(err)
	}
	return media.SendResult{ChatLog: raw}
}

func TestOutboundMatrixFileVideoAndAudioReserveOnceAndSendOnce(t *testing.T) {
	cases := []struct {
		label       string
		content     *event.MessageEventContent
		wantType    int32
		wantName    string
		wantComment string
	}{
		{
			label:    "file",
			content:  &event.MessageEventContent{MsgType: event.MsgFile, Body: "synthetic.txt", FileName: "synthetic.txt", URL: "mxc://example/file"},
			wantType: media.FileType, wantName: "synthetic.txt",
		},
		{
			label:    "legacy file without filename",
			content:  &event.MessageEventContent{MsgType: event.MsgFile, Body: "synthetic.pdf", URL: "mxc://example/file"},
			wantType: media.FileType, wantName: "synthetic.pdf",
		},
		{
			label:    "video with caption",
			content:  &event.MessageEventContent{MsgType: event.MsgVideo, Body: "synthetic video caption", FileName: "clip.mp4", URL: "mxc://example/video"},
			wantType: media.VideoType, wantName: "clip.mp4", wantComment: "synthetic video caption",
		},
		{
			label:    "audio is a file like the Mac client",
			content:  &event.MessageEventContent{MsgType: event.MsgAudio, Body: "voice.m4a", FileName: "voice.m4a", URL: "mxc://example/audio"},
			wantType: media.FileType, wantName: "voice.m4a",
		},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			data := []byte("synthetic outbound bytes for " + tc.label)
			fake := &fakeKakao{uploadResp: uploadChatLog(t, 150)}
			kc := connectedClient(t, fake)
			stubMatrixUploadDownload(t, data, nil)
			reservations := 0
			resp, err := kc.sendMatrixUpload(context.Background(), fake, &photoMatrixAPI{}, testChatID, tc.content, func(context.Context) error {
				if len(fake.uploads) != 0 {
					t.Fatal("reservation happened after the source send")
				}
				reservations++
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if reservations != 1 || len(fake.uploads) != 1 {
				t.Fatalf("reservations=%d sends=%d", reservations, len(fake.uploads))
			}
			sent := fake.uploads[0]
			if sent.Type != tc.wantType || sent.Name != tc.wantName || sent.Comment != tc.wantComment || !bytes.Equal(sent.Data, data) {
				t.Fatalf("sent type=%d name=%q comment=%q exact=%v", sent.Type, sent.Name, sent.Comment, bytes.Equal(sent.Data, data))
			}
			if resp.DB.ID != makeMessageID(testChatID, 150) {
				t.Fatalf("message id = %s", resp.DB.ID)
			}
			meta, ok := resp.DB.Metadata.(*KakaoMessageMetadata)
			if !ok || meta.Type != tc.wantType {
				t.Fatalf("metadata = %#v", resp.DB.Metadata)
			}
		})
	}
}

func TestOutboundMatrixUploadRejectsBeforeReservation(t *testing.T) {
	big := int(media.MaxUploadBytes + 1)
	cases := []struct {
		label         string
		content       *event.MessageEventContent
		downloadErr   error
		wantStatus    event.MessageStatus
		wantDownloads int
	}{
		{
			label:      "declared size over the limit",
			content:    &event.MessageEventContent{MsgType: event.MsgFile, Body: "big.bin", FileName: "big.bin", URL: "mxc://example/file", Info: &event.FileInfo{Size: big}},
			wantStatus: event.MessageStatusFail, wantDownloads: 0,
		},
		{
			label:      "denied extension",
			content:    &event.MessageEventContent{MsgType: event.MsgFile, Body: "synthetic.exe", FileName: "synthetic.exe", URL: "mxc://example/file"},
			wantStatus: event.MessageStatusFail, wantDownloads: 0,
		},
		{
			label:      "file caption",
			content:    &event.MessageEventContent{MsgType: event.MsgFile, Body: "a caption", FileName: "synthetic.txt", URL: "mxc://example/file"},
			wantStatus: event.MessageStatusFail, wantDownloads: 0,
		},
		{
			label:       "matrix download failure",
			content:     &event.MessageEventContent{MsgType: event.MsgFile, Body: "synthetic.txt", FileName: "synthetic.txt", URL: "mxc://example/file"},
			downloadErr: media.ErrUploadTooLarge,
			wantStatus:  event.MessageStatusRetriable, wantDownloads: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			fake := &fakeKakao{}
			kc := connectedClient(t, fake)
			downloads := stubMatrixUploadDownload(t, []byte("x"), tc.downloadErr)
			reservations := 0
			_, err := kc.sendMatrixUpload(context.Background(), fake, &photoMatrixAPI{}, testChatID, tc.content, func(context.Context) error {
				reservations++
				return nil
			})
			var status bridgev2.MessageStatus
			if !errors.As(err, &status) || status.Status != tc.wantStatus || !status.IsCertain || !status.SendNotice {
				t.Fatalf("err = %v status = %+v", err, status)
			}
			if reservations != 0 || len(fake.uploads) != 0 || *downloads != tc.wantDownloads {
				t.Fatalf("reservations=%d sends=%d downloads=%d", reservations, len(fake.uploads), *downloads)
			}
		})
	}
}

func TestOutboundMatrixFileReplyIsRejectedBeforeAnySend(t *testing.T) {
	fake := &fakeKakao{}
	kc := connectedClient(t, fake)
	downloads := stubMatrixUploadDownload(t, []byte("x"), nil)
	msg := matrixMessage(event.MsgFile, "synthetic.txt")
	msg.Content.FileName = "synthetic.txt"
	msg.Content.URL = "mxc://example/file"
	msg.ReplyTo = &database.Message{ID: makeMessageID(testChatID, 12)}
	_, err := kc.HandleMatrixMessage(context.Background(), msg)
	var status bridgev2.MessageStatus
	if !errors.As(err, &status) || status.Status != event.MessageStatusFail || status.ErrorReason != event.MessageStatusUnsupported || !status.IsCertain {
		t.Fatalf("err = %v status = %+v", err, status)
	}
	if len(fake.uploads) != 0 || *downloads != 0 {
		t.Fatalf("sends=%d downloads=%d", len(fake.uploads), *downloads)
	}
}

func TestOutboundMediaCapabilitiesAdvertiseFilesVideoAndAudio(t *testing.T) {
	kc := connectedClient(t, &fakeKakao{})
	caps := kc.GetCapabilities(context.Background(), nil)
	for _, typ := range []event.CapabilityMsgType{event.MsgFile, event.MsgVideo, event.MsgAudio} {
		features := caps.File[typ]
		if features == nil || features.MaxSize != media.MaxUploadBytes {
			t.Fatalf("%s features = %+v", typ, features)
		}
	}
	if caps.File[event.MsgVideo].Caption == event.CapLevelRejected || caps.File[event.MsgFile].Caption != event.CapLevelRejected {
		t.Fatal("caption support must match KakaoTalk: video only")
	}
}

func (f *fakeKakao) SendAlbum(ctx context.Context, chatID int64, photos [][]byte, caption string) (chat.WriteResponse, error) {
	f.albums = append(f.albums, sentAlbum{photos: photos, caption: caption})
	return f.albumResp, f.albumErr
}

type sentAlbum struct {
	photos  [][]byte
	caption string
}

func galleryContent(n int, caption string) *event.MessageEventContent {
	images := make([]*event.MessageEventContent, 0, n)
	for i := range n {
		images = append(images, &event.MessageEventContent{MsgType: event.MsgImage, Body: fmt.Sprintf("image-%d.png", i), URL: id.ContentURIString(fmt.Sprintf("mxc://example/image-%d", i))})
	}
	return &event.MessageEventContent{MsgType: event.MsgBeeperGallery, BeeperGalleryImages: images, BeeperGalleryCaption: caption}
}

func stubMatrixImageDownloads(t *testing.T, byURI map[id.ContentURIString][]byte) *int {
	t.Helper()
	downloads := 0
	old := matrixImageDownloader
	matrixImageDownloader = func(_ context.Context, _ bridgev2.MatrixAPI, uri id.ContentURIString, _ *event.EncryptedFileInfo) ([]byte, error) {
		downloads++
		data, ok := byURI[uri]
		if !ok {
			return nil, media.ErrInvalidImage
		}
		return append([]byte(nil), data...), nil
	}
	t.Cleanup(func() { matrixImageDownloader = old })
	return &downloads
}

func TestOutboundMatrixGalleryBecomesOneAlbumInOrder(t *testing.T) {
	first, second := connectorPNG(t), connectorJPEG(t)
	stubMatrixImageDownloads(t, map[id.ContentURIString][]byte{"mxc://example/image-0": first, "mxc://example/image-1": second})
	fake := &fakeKakao{albumResp: chat.WriteResponse{ChatID: testChatID, LogID: 160, SendAt: 1700000160}}
	kc := connectedClient(t, fake)
	reservations := 0
	resp, err := kc.sendMatrixAlbum(context.Background(), fake, &photoMatrixAPI{}, testChatID, galleryContent(2, "synthetic album caption"), func(context.Context) error {
		if len(fake.albums) != 0 {
			t.Fatal("reservation happened after the source send")
		}
		reservations++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if reservations != 1 || len(fake.albums) != 1 {
		t.Fatalf("reservations=%d sends=%d", reservations, len(fake.albums))
	}
	sent := fake.albums[0]
	if len(sent.photos) != 2 || !bytes.Equal(sent.photos[0], first) || !bytes.Equal(sent.photos[1], second) || sent.caption != "synthetic album caption" {
		t.Fatalf("album photos=%d caption=%q", len(sent.photos), sent.caption)
	}
	meta, ok := resp.DB.Metadata.(*KakaoMessageMetadata)
	if resp.DB.ID != makeMessageID(testChatID, 160) || !ok || meta.Type != media.MultiPhotoType {
		t.Fatalf("db = %+v", resp.DB)
	}
}

func TestOutboundMatrixGalleryRejectsBeforeReservation(t *testing.T) {
	png := connectorPNG(t)
	all := map[id.ContentURIString][]byte{}
	for i := range media.MaxAlbumPhotos + 1 {
		all[id.ContentURIString(fmt.Sprintf("mxc://example/image-%d", i))] = png
	}
	gif := []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")
	cases := []struct {
		label      string
		content    *event.MessageEventContent
		downloads  map[id.ContentURIString][]byte
		wantStatus event.MessageStatus
	}{
		{"more than 30 photos", galleryContent(media.MaxAlbumPhotos+1, ""), all, event.MessageStatusFail},
		{"unsupported photo format", galleryContent(2, ""), map[id.ContentURIString][]byte{"mxc://example/image-0": png, "mxc://example/image-1": gif}, event.MessageStatusFail},
		{"matrix download failure", galleryContent(2, ""), map[id.ContentURIString][]byte{"mxc://example/image-0": png}, event.MessageStatusRetriable},
		{"caption too long", galleryContent(2, strings.Repeat("a", media.MaxCaptionBytes+1)), all, event.MessageStatusFail},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			stubMatrixImageDownloads(t, tc.downloads)
			fake := &fakeKakao{}
			kc := connectedClient(t, fake)
			reservations := 0
			_, err := kc.sendMatrixAlbum(context.Background(), fake, &photoMatrixAPI{}, testChatID, tc.content, func(context.Context) error {
				reservations++
				return nil
			})
			var status bridgev2.MessageStatus
			if !errors.As(err, &status) || status.Status != tc.wantStatus || !status.IsCertain || !status.SendNotice {
				t.Fatalf("err = %v status = %+v", err, status)
			}
			if reservations != 0 || len(fake.albums) != 0 {
				t.Fatalf("reservations=%d sends=%d", reservations, len(fake.albums))
			}
		})
	}
}

func TestOutboundAlbumFailureBeforeWriteIsCertainlyNotSent(t *testing.T) {
	png := connectorPNG(t)
	stubMatrixImageDownloads(t, map[id.ContentURIString][]byte{"mxc://example/image-0": png, "mxc://example/image-1": png})
	fake := &fakeKakao{albumErr: fmt.Errorf("%w: photo 2: synthetic", client.ErrAlbumNotCreated)}
	kc := connectedClient(t, fake)
	_, err := kc.sendMatrixAlbum(context.Background(), fake, &photoMatrixAPI{}, testChatID, galleryContent(2, ""), func(context.Context) error { return nil })
	var status bridgev2.MessageStatus
	if !errors.As(err, &status) || status.Status != event.MessageStatusFail || !status.IsCertain {
		t.Fatalf("status = %+v", status)
	}
	fake.albumErr = errors.New("synthetic WRITE disconnect")
	_, err = kc.sendMatrixAlbum(context.Background(), fake, &photoMatrixAPI{}, testChatID, galleryContent(2, ""), func(context.Context) error { return nil })
	if !errors.As(err, &status) || status.IsCertain {
		t.Fatalf("ambiguous WRITE status = %+v", status)
	}
	if len(fake.albums) != 2 {
		t.Fatalf("album sends = %d, want one per Matrix event", len(fake.albums))
	}
}

func connectorJPEG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := jpeg.Encode(&out, image.NewRGBA(image.Rect(0, 0, 3, 2)), nil); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
