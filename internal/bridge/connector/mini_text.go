package connector

import (
	"context"
	"strconv"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

var errMiniTransfer = transientTransferError("connector: Mini emoticon transfer failed")

func (kc *KakaoClient) miniTextEvent(msg events.MiniTextMessage) *multipartMessage[events.MiniTextMessage] {
	meta := kc.messageMeta(msg.ChatID, msg.LogID, msg.AuthorID, msg.SentAt)
	meta.Type = bridgev2.RemoteEventMessageUpsert
	var existing map[networkid.PartID]bool
	remote := newMessage(meta, makeMessageID(msg.ChatID, msg.LogID), msg,
		func(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, value events.MiniTextMessage) (*bridgev2.ConvertedMessage, error) {
			return convertMiniText(ctx, portal, intent, value, existing)
		})
	remote.HandleExistingFunc = func(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, parts []*database.Message, _ events.MiniTextMessage) (bridgev2.UpsertResult, error) {
		existing = make(map[networkid.PartID]bool, len(parts))
		for _, p := range parts {
			existing[p.PartID] = true
		}
		for i := range msg.Parts {
			if !existing[miniTextPartID(i)] {
				return bridgev2.UpsertResult{ContinueMessageHandling: true}, nil
			}
		}
		return bridgev2.UpsertResult{}, nil
	}
	return &multipartMessage[events.MiniTextMessage]{Message: remote, expectedParts: len(msg.Parts)}
}

func miniTextPartID(i int) networkid.PartID { return networkid.PartID(strconv.Itoa(i)) }

func convertMiniText(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg events.MiniTextMessage, existing map[networkid.PartID]bool) (*bridgev2.ConvertedMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, matrixImageTransferTimeout)
	defer cancel()
	type resourceState struct {
		resource media.StickerResource
		category string
		uploaded bool
		uri      id.ContentURIString
		file     *event.EncryptedFileInfo
	}
	cache := map[string]*resourceState{}
	totalBytes := 0
	budgetExceeded := false
	// Resolve the remaining resources before uploading. A deterministic budget
	// failure must not turn into endless retries with orphaned media uploads.
	for i, part := range msg.Parts {
		if existing[miniTextPartID(i)] || part.ResourceID == "" || cache[part.ResourceID] != nil {
			continue
		}
		state := &resourceState{}
		if budgetExceeded {
			state.category = "resource_budget"
		} else {
			resource, err := media.DownloadMini(ctx, stickerHTTPClient, part.ResourceID)
			if err != nil {
				category, ok := stickerResourceFailure(err)
				if !ok {
					return nil, errMiniTransfer
				}
				state.category = category
			} else {
				totalBytes += len(resource.Data)
				if totalBytes > 8<<20 {
					budgetExceeded = true
				} else {
					state.resource = resource
				}
			}
		}
		cache[part.ResourceID] = state
	}
	if budgetExceeded {
		for _, state := range cache {
			state.category = "resource_budget"
			state.resource = media.StickerResource{}
		}
	}
	converted := &bridgev2.ConvertedMessage{}
	for i, source := range msg.Parts {
		partID := miniTextPartID(i)
		if existing[partID] {
			continue
		}
		meta := newKakaoMessageMetadata(msg.ChatID, msg.LogID, msg.AuthorID, messagetype.Text, msg.Message, 0)
		content := &event.MessageEventContent{MsgType: event.MsgText, Body: source.Text}
		if source.ResourceID != "" {
			state := cache[source.ResourceID]
			if state.category != "" {
				meta.ConversionGap = state.category
				content = &event.MessageEventContent{MsgType: event.MsgNotice, Body: source.Text + " [KakaoTalk Mini emoticon unavailable: " + state.category + "]"}
			} else {
				if !state.uploaded {
					uri, file, err := intent.UploadMedia(ctx, portal.MXID, state.resource.Data, "mini.png", state.resource.MIME)
					if err != nil {
						return nil, errMiniTransfer
					}
					state.uri = uri
					state.file = file
					state.uploaded = true
				}
				resource := state.resource
				content = &event.MessageEventContent{MsgType: event.MsgImage, Body: source.Text, FileName: "mini.png", URL: state.uri, File: state.file, Info: &event.FileInfo{MimeType: resource.MIME, Size: len(resource.Data), Width: resource.Width, Height: resource.Height}}
				if state.file != nil {
					content.URL = ""
				}
			}
		}
		converted.Parts = append(converted.Parts, &bridgev2.ConvertedMessagePart{ID: partID, Type: event.EventMessage, Content: content, DBMetadata: meta})
	}
	return converted, nil
}
