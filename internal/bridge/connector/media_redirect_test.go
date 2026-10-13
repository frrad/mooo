package connector

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/protocol/media"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestOutboundRedirectReportsAttachmentCertainlyNotSent(t *testing.T) {
	fake := &fakeKakao{imageErr: media.ErrUploadRedirect}
	kc := connectedClient(t, fake)
	old := matrixImageDownloader
	data := connectorPNG(t)
	matrixImageDownloader = func(context.Context, bridgev2.MatrixAPI, id.ContentURIString, *event.EncryptedFileInfo) ([]byte, error) {
		return data, nil
	}
	t.Cleanup(func() { matrixImageDownloader = old })
	reservations := 0
	response, err := kc.sendMatrixImage(context.Background(), fake, &photoMatrixAPI{}, testChatID, imageContent("mxc://example/image", nil), func(context.Context) error { reservations++; return nil })
	var status bridgev2.MessageStatus
	if response != nil || !errors.Is(err, media.ErrUploadRedirect) || !errors.As(err, &status) {
		t.Fatalf("response=%v error=%v", response, err)
	}
	if !status.IsCertain || status.Status != event.MessageStatusFail || status.ErrorReason != event.MessageStatusUnsupported || !strings.Contains(status.Message, "was not sent") {
		t.Fatalf("redirect status %+v", status)
	}
	if reservations != 1 || fake.imageCalls != 1 {
		t.Fatalf("reservations %d sends %d", reservations, fake.imageCalls)
	}
}
