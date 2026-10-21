package connector

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
)

var (
	// errLeverageReplyTarget refuses a Matrix reply to a locked or adult-only
	// card, on which the official client offers no Reply (research/leverage.md).
	errLeverageReplyTarget = errors.New("connector: reply target is a locked or adult-only KakaoTalk card")
	// errLeverageReactionTarget refuses a Matrix reaction to a carousel card,
	// on which the official client offers no reaction.
	errLeverageReactionTarget = errors.New("connector: reaction target is a KakaoTalk carousel card")
	// errLeverageDeleteNotSupported refuses deleting a card for everyone,
	// which is untraced in the official client.
	errLeverageDeleteNotSupported = errors.New("connector: KakaoTalk cards cannot be deleted from Matrix")
)

// leveragePartIDs keeps the summary as the default reply target and gives
// every card/image a stable identity for partial-delivery recovery.
func leveragePartIDs(msg events.LeverageMessage) []networkid.PartID {
	parts := []networkid.PartID{""}
	for i, card := range msg.Cards {
		parts = append(parts, networkid.PartID(fmt.Sprintf("card-%d", i)))
		for j := range card.Images {
			parts = append(parts, networkid.PartID(fmt.Sprintf("card-%d-image-%d", i, j)))
		}
	}
	return parts
}

func convertLeverage(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg events.LeverageMessage, existing map[networkid.PartID]bool) (*bridgev2.ConvertedMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, matrixImageTransferTimeout)
	defer cancel()
	metadata := func() *KakaoMessageMetadata {
		m := newKakaoMessageMetadata(msg.ChatID, msg.LogID, msg.AuthorID, messagetype.Leverage, msg.Summary, 0)
		m.ReplyUnavailable = msg.ReplyUnavailable
		m.ReactionUnavailable = msg.ReactionUnavailable
		return m
	}
	converted := &bridgev2.ConvertedMessage{}
	add := func(partID networkid.PartID, body string) {
		if existing[partID] {
			return
		}
		part := messageWithMetadata(event.MsgText, body, metadata()).Parts[0]
		part.ID = partID
		converted.Parts = append(converted.Parts, part)
	}
	body := msg.Summary
	if msg.Ad && len(msg.Cards) > 0 {
		body = "[Advertisement]\n" + body
	}
	if msg.Link != "" {
		body += "\n" + msg.Link
	}
	add("", body)
	actionText := func(a events.LeverageAction) string {
		if a.URL != "" {
			return a.Label + ": " + a.URL
		}
		if a.OpenInKakao {
			return a.Label + ": Open in KakaoTalk"
		}
		return a.Label + ": Action unavailable in this bridge"
	}
	for i, card := range msg.Cards {
		body := fmt.Sprintf("Card %d/%d", i+1, len(msg.Cards))
		if card.Heading != "" {
			body += "\n" + card.Heading
		}
		if card.Description != "" {
			body += "\n" + card.Description
		}
		for _, a := range card.Actions {
			body += "\n" + actionText(a)
		}
		add(networkid.PartID(fmt.Sprintf("card-%d", i)), body)
		for j, image := range card.Images {
			partID := networkid.PartID(fmt.Sprintf("card-%d-image-%d", i, j))
			if existing[partID] {
				continue
			}
			caption := fmt.Sprintf("Card %d image %d", i+1, j+1)
			if image.Action.URL != "" || image.Action.OpenInKakao {
				a := image.Action
				a.Label = "Image action"
				caption += "\n" + actionText(a)
			}
			// Thumbnail URLs are uncredentialed. Reuse the bounded CDN image
			// downloader, including redirect and image validation. Other hosts remain
			// visible links and are never fetched by the bridge.
			if media.ValidateAvatarURL(image.URL) != nil {
				add(partID, caption+"\nImage: "+image.URL)
				continue
			}
			data, err := historyResourceBytes(ctx, []any{"leverage-image", image.URL}, func(ctx context.Context) ([]byte, error) {
				return media.DownloadAvatar(ctx, historyHTTPClient(photoHTTPClient), image.URL)
			})
			if err != nil {
				if ctx.Err() != nil {
					return nil, errLeverageTransfer
				}
				add(partID, caption+"\nImage unavailable: "+image.URL)
				continue
			}
			mime := http.DetectContentType(data)
			name := "card.jpg"
			if mime == "image/png" {
				name = "card.png"
			}
			uri, file, err := intent.UploadMedia(ctx, portal.MXID, data, name, mime)
			if err != nil {
				return nil, errLeverageTransfer
			}
			content := &event.MessageEventContent{MsgType: event.MsgImage, Body: caption, FileName: name, URL: uri, File: file, Info: &event.FileInfo{Width: image.Width, Height: image.Height, MimeType: mime, Size: len(data)}}
			if file != nil {
				content.URL = ""
			}
			converted.Parts = append(converted.Parts, &bridgev2.ConvertedMessagePart{ID: partID, Type: event.EventMessage, Content: content, DBMetadata: metadata()})
		}
	}
	return converted, nil
}

var errLeverageTransfer = transientTransferError("connector: card image transfer failed")
