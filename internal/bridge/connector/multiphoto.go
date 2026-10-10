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
		metadata := newKakaoMessageMetadata(a.ChatID, a.LogID, a.AuthorID, messagetype.MultiPhoto, "[album]", 0)
		data, err := media.DownloadAlbumPhoto(ctx, photoHTTPClient, photo)
		if err != nil {
			if category, ok := deterministicPhotoFailure(err); ok {
				metadata.ConversionGap = category
				converted.Parts = append(converted.Parts, &bridgev2.ConvertedMessagePart{ID: partID, Type: event.EventMessage, Content: &event.MessageEventContent{MsgType: event.MsgNotice, Body: fmt.Sprintf("A KakaoTalk album photo could not be displayed (%s).", category)}, DBMetadata: metadata})
				continue
			}
			return nil, errPhotoTransfer
		}
		name := fmt.Sprintf("photo-%d.jpg", i+1)
		if photo.MediaType == "image/png" {
			name = fmt.Sprintf("photo-%d.png", i+1)
		}
		uri, file, err := intent.UploadMedia(ctx, portal.MXID, data, name, photo.MediaType)
		if err != nil {
			return nil, errPhotoTransfer
		}
		content := &event.MessageEventContent{MsgType: event.MsgImage, Body: name, FileName: name, URL: uri, File: file, Info: &event.FileInfo{MimeType: photo.MediaType, Size: len(data), Width: int(photo.Width), Height: int(photo.Height)}}
		if i < len(a.Comments) && a.Comments[i] != "" {
			content.Body = a.Comments[i]
		}
		if file != nil {
			content.URL = ""
		}
		converted.Parts = append(converted.Parts, &bridgev2.ConvertedMessagePart{ID: partID, Type: event.EventMessage, Content: content, DBMetadata: metadata})
	}
	return converted, nil
}
