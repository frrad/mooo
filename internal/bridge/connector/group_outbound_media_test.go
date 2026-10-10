package connector

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

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
