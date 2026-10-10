package connector

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

var stickerHTTPClient = http.DefaultClient
var errStickerTransfer = transientTransferError("connector: Kakao sticker transfer failed")

func convertSticker(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg events.StickerMessage) (*bridgev2.ConvertedMessage, error) {
	part, err := stickerPart(ctx, portal, intent, msg)
	if err != nil {
		return nil, err
	}
	return &bridgev2.ConvertedMessage{Parts: []*bridgev2.ConvertedMessagePart{part}}, nil
}

// stickerPart downloads one sticker resource and returns its Matrix part, or a
// notice part for a deterministic resource failure.
func stickerPart(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg events.StickerMessage) (*bridgev2.ConvertedMessagePart, error) {
	ctx, cancel := context.WithTimeout(ctx, matrixImageTransferTimeout)
	defer cancel()
	resource, err := media.DownloadSticker(ctx, stickerHTTPClient, msg.Attachment)
	if err != nil {
		category := ""
		switch {
		case errors.Is(err, media.ErrStickerUnavailable):
			category = "unavailable"
		case errors.Is(err, media.ErrUnsupportedSticker):
			category = "unsupported"
		case errors.Is(err, media.ErrInvalidSticker), errors.Is(err, media.ErrInvalidStickerResource):
			category = "invalid_resource"
		default:
			return nil, errStickerTransfer
		}
		metadata := newKakaoMessageMetadata(msg.ChatID, msg.LogID, msg.AuthorID, msg.Type, "[sticker unavailable]", 0)
		metadata.ConversionGap = category
		return messageWithMetadata(event.MsgNotice, fmt.Sprintf("A KakaoTalk sticker could not be displayed (%s).", category), metadata).Parts[0], nil
	}
	uri, file, err := intent.UploadMedia(ctx, portal.MXID, resource.Data, "sticker"+resource.Extension, resource.MIME)
	if err != nil {
		return nil, errStickerTransfer
	}
	content := &event.MessageEventContent{Body: "KakaoTalk sticker", URL: uri, File: file, Info: &event.FileInfo{MimeType: resource.MIME, Size: len(resource.Data), Width: resource.Width, Height: resource.Height}}
	if file != nil {
		content.URL = ""
	}
	return &bridgev2.ConvertedMessagePart{Type: event.EventSticker, Content: content, DBMetadata: newKakaoMessageMetadata(msg.ChatID, msg.LogID, msg.AuthorID, msg.Type, "[sticker]", 0)}, nil
}
