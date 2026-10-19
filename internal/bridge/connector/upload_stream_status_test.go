package connector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/media"
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
	if got := kakaoUploadTimeout(media.MaxUploadBytes); got < 17*time.Minute {
		t.Fatalf("kakaoUploadTimeout(64 MiB) = %v, want at least 17m", got)
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
	if got := fake.uploadDeadline.Sub(started); got < kakaoUploadTimeout(len(data))-time.Minute || got <= matrixUploadTransferTimeout {
		t.Fatalf("SendUpload deadline %v after start, want about %v", got, kakaoUploadTimeout(len(data)))
	}
}
