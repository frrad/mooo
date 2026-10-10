package connector

import (
	"context"
	"net/http"
	"strings"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

var errFileTransfer = transientTransferError("connector: file transfer failed")

func convertFile(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg events.FileMessage) (*bridgev2.ConvertedMessage, error) {
	f := msg.Message
	a := f.Attachment
	return convertAttachment(ctx, portal, intent, attachmentSpec{
		kind: "file", failedTo: "downloaded", msgType: event.MsgFile,
		metadata:    newKakaoMessageMetadata(f.ChatID, f.LogID, f.AuthorID, messagetype.File, "[file]", 0),
		transferErr: errFileTransfer,
		download: func(ctx context.Context) (attachmentMedia, error) {
			data, err := media.DownloadFile(ctx, photoHTTPClient, a)
			if err != nil {
				return attachmentMedia{}, err
			}
			mimeType := strings.SplitN(http.DetectContentType(data), ";", 2)[0]
			return attachmentMedia{data: data, name: a.Name, mime: mimeType}, nil
		},
	})
}
