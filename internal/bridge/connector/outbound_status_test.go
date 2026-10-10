package connector

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chat"
)

// A send that may have reached Kakao must not look retriable in Matrix: a
// manual resend could duplicate a delivered message.
func TestAmbiguousOutboundTextIsReportedAsUnknownNotRetriable(t *testing.T) {
	sendErr := errors.New("connection reset during write")
	fake := &fakeKakao{sendErr: sendErr}
	kc := connectedClient(t, fake)

	_, err := kc.HandleMatrixMessage(context.Background(), matrixMessage(event.MsgText, "maybe delivered"))

	if !errors.Is(err, sendErr) {
		t.Fatalf("lost the underlying cause: %v", err)
	}
	status := bridgev2.WrapErrorInStatus(err)
	if status.Status != event.MessageStatusFail {
		t.Fatalf("status = %s, want %s", status.Status, event.MessageStatusFail)
	}
	if status.IsCertain {
		t.Fatal("an unconfirmed send was reported as a certain failure")
	}
	if !status.SendNotice || !bytes.Contains([]byte(status.Message), []byte("Check KakaoTalk before sending it again")) {
		t.Fatalf("no actionable notice: %+v", status)
	}
	if len(fake.sends) != 1 {
		t.Fatalf("sent %d times", len(fake.sends))
	}
}

func TestAcceptedOutboundTextWithoutLogIDIsNotRetriable(t *testing.T) {
	fake := &fakeKakao{sendResp: chat.WriteResponse{LogID: 0}}
	kc := connectedClient(t, fake)

	_, err := kc.HandleMatrixMessage(context.Background(), matrixMessage(event.MsgText, "accepted"))

	status := bridgev2.WrapErrorInStatus(err)
	if err == nil || status.Status != event.MessageStatusFail || status.IsCertain {
		t.Fatalf("accepted send reported as %+v (%v)", status, err)
	}
}

// A server status reply means Kakao processed and refused the command, so the
// failure is certain and nothing was delivered.
func TestRefusedOutboundTextIsACertainFailure(t *testing.T) {
	fake := &fakeKakao{sendErr: client.StatusError{Command: "WRITE", Status: -500}}
	kc := connectedClient(t, fake)

	_, err := kc.HandleMatrixMessage(context.Background(), matrixMessage(event.MsgText, "refused"))

	status := bridgev2.WrapErrorInStatus(err)
	if status.Status != event.MessageStatusFail || !status.IsCertain {
		t.Fatalf("refusal reported as %+v", status)
	}
}

func TestAmbiguousOutboundImageIsReportedAsUnknownNotRetriable(t *testing.T) {
	fake := &fakeKakao{imageErr: errors.New("upload stream reset")}
	kc := connectedClient(t, fake)
	oldDownloader := matrixImageDownloader
	matrixImageDownloader = func(context.Context, bridgev2.MatrixAPI, id.ContentURIString, *event.EncryptedFileInfo) ([]byte, error) {
		return []byte("image"), nil
	}
	t.Cleanup(func() { matrixImageDownloader = oldDownloader })

	_, err := kc.sendMatrixImage(context.Background(), fake, &photoMatrixAPI{}, 3000, "mxc://example/image", nil)

	status := bridgev2.WrapErrorInStatus(err)
	if status.Status != event.MessageStatusFail || status.IsCertain || !status.SendNotice {
		t.Fatalf("ambiguous image reported as %+v", status)
	}
	if fake.imageCalls != 1 {
		t.Fatalf("image calls = %d", fake.imageCalls)
	}
}
