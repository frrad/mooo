package connector

import (
	"context"
	"errors"
	"fmt"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

var errContactTransfer = errors.New("connector: contact transfer failed")

func convertContact(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg events.ContactMessage) (*bridgev2.ConvertedMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, matrixImageTransferTimeout)
	defer cancel()
	f := msg.Message
	a := f.Attachment
	metadata := newKakaoMessageMetadata(f.ChatID, f.LogID, f.AuthorID, messagetype.Contact, "[contact]", 0)
	data, err := media.DownloadContact(ctx, photoHTTPClient, a)
	if err != nil {
		if category, ok := deterministicPhotoFailure(err); ok {
			metadata.ConversionGap = category
			return messageWithMetadata(event.MsgNotice, fmt.Sprintf("A KakaoTalk contact could not be downloaded (%s).", category), metadata), nil
		}
		return nil, errContactTransfer
	}
	mimeType := "text/vcard"
	uri, contact, err := intent.UploadMedia(ctx, portal.MXID, data, "contact.vcf", mimeType)
	if err != nil {
		return nil, errContactTransfer
	}
	content := &event.MessageEventContent{MsgType: event.MsgFile, Body: "KakaoTalk contact: " + a.Name, FileName: "contact.vcf", URL: uri, File: contact, Info: &event.FileInfo{MimeType: mimeType, Size: len(data)}}
	if contact != nil {
		content.URL = ""
	}
	return &bridgev2.ConvertedMessage{Parts: []*bridgev2.ConvertedMessagePart{{Type: event.EventMessage, Content: content, DBMetadata: metadata}}}, nil
}
