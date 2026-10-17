package connector

import (
	"context"
	"errors"
	"net/http"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

var stickerHTTPClient = http.DefaultClient
var errStickerTransfer = transientTransferError("connector: Kakao sticker transfer failed")

// stickerTextPartID is the part carrying the text sent with a sticker. The
// sticker keeps the empty part ID it has when sent alone.
const stickerTextPartID networkid.PartID = "text"

// stickerEvent maps a sticker alone to one sticker part and a sticker sent
// with text to the sticker followed by the text, as both official clients
// render it.
func (kc *KakaoClient) stickerEvent(msg events.StickerMessage) bridgev2.RemoteEvent {
	if msg.Message == "" {
		return messageEvent(kc, msg.Position(), msg.AuthorID, msg.SentAt, msg, convertSticker)
	}
	return namedPartsEvent(kc, msg.Position(), msg.AuthorID, msg.SentAt, msg, []networkid.PartID{"", stickerTextPartID}, convertStickerWithText)
}

func convertStickerWithText(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg events.StickerMessage, existing map[networkid.PartID]bool) (*bridgev2.ConvertedMessage, error) {
	converted := &bridgev2.ConvertedMessage{}
	if !existing[""] {
		part, err := stickerPart(ctx, portal, intent, msg)
		if err != nil {
			return nil, err
		}
		// A reply to this message quotes its text (observed).
		part.DBMetadata.(*KakaoMessageMetadata).Preview = truncateReplyPreview(msg.Message)
		converted.Parts = append(converted.Parts, part)
	}
	if !existing[stickerTextPartID] {
		text := messageWithMetadata(event.MsgText, msg.Message, newKakaoMessageMetadata(msg.ChatID, msg.LogID, msg.AuthorID, msg.Type, msg.Message, 0)).Parts[0]
		text.ID = stickerTextPartID
		converted.Parts = append(converted.Parts, text)
	}
	return converted, nil
}

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
	return attachmentPart(ctx, portal, intent, attachmentSpec{
		kind: "sticker", failedTo: "displayed", eventType: event.EventSticker, omitFileName: true, body: "KakaoTalk sticker",
		metadata:      newKakaoMessageMetadata(msg.ChatID, msg.LogID, msg.AuthorID, msg.Type, "[sticker]", 0),
		gapMetadata:   newKakaoMessageMetadata(msg.ChatID, msg.LogID, msg.AuthorID, msg.Type, "[sticker unavailable]", 0),
		deterministic: stickerResourceFailure,
		transferErr:   errStickerTransfer,
		download: func(ctx context.Context) (attachmentMedia, error) {
			resource, err := historyResourceJSON(ctx, []any{"Sticker", msg.Attachment}, func(ctx context.Context) (media.StickerResource, error) {
				return media.DownloadSticker(ctx, historyHTTPClient(stickerHTTPClient), msg.Attachment)
			})
			if err != nil {
				return attachmentMedia{}, err
			}
			info := event.FileInfo{Width: resource.Width, Height: resource.Height}
			return attachmentMedia{data: resource.Data, name: "sticker" + resource.Extension, mime: resource.MIME, info: info}, nil
		},
	})
}

// stickerResourceFailure maps a deterministic sticker or Mini emoticon
// resource failure to its conversion-gap category.
func stickerResourceFailure(err error) (string, bool) {
	switch {
	case errors.Is(err, media.ErrStickerUnavailable):
		return "unavailable", true
	case errors.Is(err, media.ErrUnsupportedSticker):
		return "unsupported", true
	case errors.Is(err, media.ErrInvalidSticker), errors.Is(err, media.ErrInvalidStickerResource):
		return "invalid_resource", true
	default:
		return "", false
	}
}
