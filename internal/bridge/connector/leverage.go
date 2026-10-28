package connector

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"

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
// every card a stable identity for partial-delivery recovery. A card's first
// image carries the card's text as its caption, so only further images have
// parts of their own.
func leveragePartIDs(msg events.LeverageMessage) []networkid.PartID {
	parts := []networkid.PartID{""}
	for i, card := range msg.Cards {
		parts = append(parts, networkid.PartID(fmt.Sprintf("card-%d", i)))
		for j := 1; j < len(card.Images); j++ {
			parts = append(parts, networkid.PartID(fmt.Sprintf("card-%d-image-%d", i, j)))
		}
	}
	return parts
}

// leverageDim is a grey that reads on both light and dark themes.
const leverageDim = `<span data-mx-color="#8e8e93">`

// leverageLines builds a message's body and formatted_body line by line.
type leverageLines struct{ body, html []string }

func (l *leverageLines) add(body, html string) {
	l.body = append(l.body, body)
	l.html = append(l.html, html)
}
func (l *leverageLines) plain(s string) { l.add(s, htmlLines(s)) }
func (l *leverageLines) link(label, url string) {
	l.add(label+": "+url, `<a href="`+html.EscapeString(url)+`">`+html.EscapeString(label)+"</a>")
}
func (l leverageLines) content(msgType event.MessageType) *event.MessageEventContent {
	return &event.MessageEventContent{MsgType: msgType, Body: strings.Join(l.body, "\n"), Format: event.FormatHTML, FormattedBody: strings.Join(l.html, "<br>")}
}

// leverageSummaryLines renders a carousel's summary as a bold title. The
// summary text is kept as sent: an ad's own "(광고)" is not stripped, the AD
// flag only adds a dim "Ad ·" prefix.
func leverageSummaryLines(msg events.LeverageMessage) leverageLines {
	var l leverageLines
	body, prefix := msg.Summary, ""
	if msg.Ad {
		body, prefix = "Ad · "+body, leverageDim+"Ad · </span>"
	}
	l.add(body, prefix+"<strong>"+htmlLines(msg.Summary)+"</strong>")
	if msg.Link != "" {
		l.add(msg.Link, `<a href="`+html.EscapeString(msg.Link)+`">`+html.EscapeString(msg.Link)+"</a>")
	}
	return l
}

// leverageCardLines renders a card as a dim "i/n ·" counter and bold
// heading, its description, and its buttons: web buttons as links, buttons
// that only work in the KakaoTalk app as dim italic labels. Buttons with
// neither are left out.
func leverageCardLines(card events.LeverageCard, i, n int) leverageLines {
	var l leverageLines
	counter := fmt.Sprintf("%d/%d", i+1, n)
	if card.Heading != "" {
		l.add(counter+" · "+card.Heading, leverageDim+counter+" · </span><strong>"+htmlLines(card.Heading)+"</strong>")
	} else {
		l.add(counter, leverageDim+counter+"</span>")
	}
	if card.Description != "" {
		l.plain(card.Description)
	}
	for _, a := range card.Actions {
		switch {
		case a.URL != "":
			l.link(a.Label, a.URL)
		case a.OpenInKakao:
			label := a.Label + " (KakaoTalk only)"
			l.add(label, "<em>"+leverageDim+html.EscapeString(label)+"</span></em>")
		}
	}
	return l
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
	add := func(partID networkid.PartID, content *event.MessageEventContent) {
		converted.Parts = append(converted.Parts, &bridgev2.ConvertedMessagePart{ID: partID, Type: event.EventMessage, Content: content, DBMetadata: metadata()})
	}
	if !existing[""] {
		if len(msg.Cards) == 0 {
			body := msg.Summary
			if msg.Link != "" {
				body += "\n" + msg.Link
			}
			part := messageWithMetadata(event.MsgText, body, metadata()).Parts[0]
			converted.Parts = append(converted.Parts, part)
		} else {
			add("", leverageSummaryLines(msg).content(event.MsgText))
		}
	}
	for i, card := range msg.Cards {
		for j, image := range card.Images {
			partID := networkid.PartID(fmt.Sprintf("card-%d-image-%d", i, j))
			var caption leverageLines
			if j == 0 {
				partID = networkid.PartID(fmt.Sprintf("card-%d", i))
				caption = leverageCardLines(card, i, len(msg.Cards))
			}
			if existing[partID] {
				continue
			}
			// Thumbnail URLs are uncredentialed. Reuse the bounded CDN image
			// downloader, including redirect and image validation. Other hosts remain
			// visible links and are never fetched by the bridge.
			if media.ValidateAvatarURL(image.URL) != nil {
				caption.link("Image", image.URL)
				add(partID, caption.content(event.MsgText))
				continue
			}
			data, err := historyResourceBytes(ctx, []any{"leverage-image", image.URL}, func(ctx context.Context) ([]byte, error) {
				return media.DownloadAvatar(ctx, historyHTTPClient(photoHTTPClient), image.URL)
			})
			if err != nil {
				if ctx.Err() != nil {
					return nil, errLeverageTransfer
				}
				caption.link("Image unavailable", image.URL)
				add(partID, caption.content(event.MsgText))
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
			// A body that differs from the file name is the image's caption.
			content := &event.MessageEventContent{MsgType: event.MsgImage, Body: name, FileName: name, URL: uri, File: file, Info: &event.FileInfo{Width: image.Width, Height: image.Height, MimeType: mime, Size: len(data)}}
			if len(caption.body) > 0 {
				text := caption.content(event.MsgImage)
				content.Body, content.Format, content.FormattedBody = text.Body, text.Format, text.FormattedBody
			}
			if file != nil {
				content.URL = ""
			}
			add(partID, content)
		}
		if len(card.Images) == 0 {
			partID := networkid.PartID(fmt.Sprintf("card-%d", i))
			if !existing[partID] {
				add(partID, leverageCardLines(card, i, len(msg.Cards)).content(event.MsgText))
			}
		}
	}
	return converted, nil
}

var errLeverageTransfer = transientTransferError("connector: card image transfer failed")
