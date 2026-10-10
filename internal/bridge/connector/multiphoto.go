package connector

import (
	"context"
	"fmt"
	"strconv"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

func (kc *KakaoClient) albumEvent(msg events.MultiPhotoMessage) *multipartMessage[events.MultiPhotoMessage] {
	a := msg.Message
	meta := kc.messageMeta(a.ChatID, a.LogID, a.AuthorID, a.SentAt)
	meta.Type = bridgev2.RemoteEventMessageUpsert
	var existingIDs map[networkid.PartID]bool
	remote := newMessage(meta, makeMessageID(a.ChatID, a.LogID), msg,
		func(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, value events.MultiPhotoMessage) (*bridgev2.ConvertedMessage, error) {
			return convertAlbum(ctx, portal, intent, value, existingIDs)
		})
	// bridgev2 normally treats any existing part as a complete duplicate. Albums
	// must resume missing parts after a partially successful Matrix send.
	remote.HandleExistingFunc = func(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, parts []*database.Message, _ events.MultiPhotoMessage) (bridgev2.UpsertResult, error) {
		existingIDs = make(map[networkid.PartID]bool, len(parts))
		for _, part := range parts {
			// An album sent from Matrix is recorded as one whole-message
			// row; it is already complete in Matrix.
			if part.PartID == "" {
				return bridgev2.UpsertResult{}, nil
			}
			existingIDs[part.PartID] = true
		}
		for i := range a.Photos {
			if !existingIDs[albumPartID(i)] {
				return bridgev2.UpsertResult{ContinueMessageHandling: true}, nil
			}
		}
		return bridgev2.UpsertResult{}, nil
	}
	return &multipartMessage[events.MultiPhotoMessage]{Message: remote, expectedParts: len(a.Photos)}
}

func albumPartID(index int) networkid.PartID { return networkid.PartID(strconv.Itoa(index)) }

func convertAlbum(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg events.MultiPhotoMessage, existing map[networkid.PartID]bool) (*bridgev2.ConvertedMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, matrixImageTransferTimeout)
	defer cancel()
	a := msg.Message
	converted := &bridgev2.ConvertedMessage{}
	for i, photo := range a.Photos {
		partID := albumPartID(i)
		if existing[partID] {
			continue
		}
		comment := ""
		if i < len(a.Comments) {
			comment = a.Comments[i]
		}
		ext, mimeType := matrixPhotoType(photo.MediaType)
		part, err := attachmentPart(ctx, portal, intent, attachmentSpec{
			kind: "album photo", failedTo: "displayed", msgType: event.MsgImage, body: comment,
			metadata:    newKakaoMessageMetadata(a.ChatID, a.LogID, a.AuthorID, messagetype.MultiPhoto, "[album]", 0),
			transferErr: errPhotoTransfer,
			download: func(ctx context.Context) (attachmentMedia, error) {
				data, err := media.DownloadAlbumPhoto(ctx, photoHTTPClient, photo)
				info := event.FileInfo{Width: int(photo.Width), Height: int(photo.Height)}
				return attachmentMedia{data: data, name: fmt.Sprintf("photo-%d.%s", i+1, ext), mime: mimeType, info: info}, err
			},
		})
		if err != nil {
			return nil, err
		}
		part.ID = partID
		converted.Parts = append(converted.Parts, part)
	}
	return converted, nil
}
