package connector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// frrad/mooo#428: the encrypt failure came after SHIP and POST but before
// any byte was streamed, and the user was told the file "may have been
// delivered". An upload that fails before streaming is certainly not sent.
func TestUploadFailureBeforeStreamingReportsCertainlyNotSent(t *testing.T) {
	fake := &fakeKakao{uploadErr: errors.Join(client.ErrUploadNotSent, errors.New("client: media connect: synthetic"))}
	kc := connectedClient(t, fake)
	stubMatrixUploadDownload(t, []byte("synthetic video bytes"), nil)
	content := &event.MessageEventContent{MsgType: event.MsgVideo, Body: "clip.mp4", FileName: "clip.mp4", URL: "mxc://example/video"}
	response, err := kc.sendMatrixUpload(context.Background(), fake, &photoMatrixAPI{}, testChatID, content, func(context.Context) error { return nil })
	var status bridgev2.MessageStatus
	if response != nil || !errors.As(err, &status) {
		t.Fatalf("response=%v error=%v", response, err)
	}
	if !status.IsCertain || status.Status != event.MessageStatusFail || !strings.Contains(status.Message, "was not sent") || strings.Contains(status.Message, "may have been delivered") {
		t.Fatalf("not-sent upload status %+v", status)
	}
}

// A refusal status keeps its own message even though it also failed before
// streaming, and a failure after streaming stays ambiguous.
func TestUploadStatusAndStreamFailuresKeepTheirClassification(t *testing.T) {
	var status bridgev2.MessageStatus
	refused := outboundSendError(errors.Join(client.ErrUploadNotSent, client.StatusError{Command: "POST", Status: -500}))
	if !errors.As(refused, &status) || !status.IsCertain || status.Message != "KakaoTalk refused this message." {
		t.Fatalf("POST refusal status %v", refused)
	}
	ambiguous := outboundSendError(errors.New("client: media COMPLETE: synthetic timeout"))
	if !errors.As(ambiguous, &status) || status.IsCertain || !strings.Contains(status.Message, "may have been delivered") {
		t.Fatalf("ambiguous status %v", ambiguous)
	}
}

// The Kakao send of a file runs under its own deadline, sized for a slow but
// progressing upload, not the 3-minute Matrix fetch deadline: a 64 MiB file
// at the 64 KiB/s floor needs about 17 minutes.
func TestUploadSendDeadlineGrowsWithFileSize(t *testing.T) {
	if got := kakaoUploadTimeout(media.Upload{Type: messagetype.File, Data: make([]byte, media.MaxUploadBytes)}); got < 17*time.Minute {
		t.Fatalf("kakaoUploadTimeout(64 MiB file) = %v, want at least 17m", got)
	}
	data := make([]byte, 8<<20)
	fake := &fakeKakao{uploadResp: uploadChatLog(t, 150)}
	kc := connectedClient(t, fake)
	stubMatrixUploadDownload(t, data, nil)
	content := &event.MessageEventContent{MsgType: event.MsgFile, Body: "synthetic.dat", FileName: "synthetic.dat", URL: "mxc://example/file"}
	started := time.Now()
	if _, err := kc.sendMatrixUpload(context.Background(), fake, &photoMatrixAPI{}, testChatID, content, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if fake.uploadDeadline.IsZero() {
		t.Fatal("SendUpload ran without a deadline")
	}
	want := kakaoUploadTimeout(fake.uploads[0])
	if got := fake.uploadDeadline.Sub(started); got < want-time.Minute || got <= matrixUploadTransferTimeout {
		t.Fatalf("SendUpload deadline %v after start, want about %v", got, want)
	}
}

// A redirected video may wait up to 10 minutes for the server to process it
// before WRITEMEDIA, so its backstop allows for that on top of the transfer.
func TestUploadSendDeadlineAllowsVideoReadyWait(t *testing.T) {
	file := kakaoUploadTimeout(media.Upload{Type: messagetype.File, Data: make([]byte, 1<<20)})
	video := kakaoUploadTimeout(media.Upload{Type: messagetype.Video, Data: make([]byte, 1<<20)})
	if video-file != 10*time.Minute {
		t.Fatalf("video backstop %v, file %v: want 10m more for the ready wait", video, file)
	}
}

// The server's content filter refusal during the ready wait is a certain
// refusal with its own reason, not "may have been delivered".
func TestUploadContentFilterReportsCertainRefusal(t *testing.T) {
	err := outboundSendError(errors.Join(client.ErrUploadNotSent, media.ErrUploadFiltered))
	var status bridgev2.MessageStatus
	if !errors.As(err, &status) || !status.IsCertain || !strings.Contains(status.Message, "refused the content") {
		t.Fatalf("content filter status %v", err)
	}
}
