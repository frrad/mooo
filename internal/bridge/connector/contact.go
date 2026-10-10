package connector

import (
	"context"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

var errContactTransfer = transientTransferError("connector: contact transfer failed")

func convertContact(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg events.ContactMessage) (*bridgev2.ConvertedMessage, error) {
	f := msg.Message
	a := f.Attachment
	return convertAttachment(ctx, portal, intent, attachmentSpec{
		kind: "contact", failedTo: "downloaded", msgType: event.MsgFile, body: "KakaoTalk contact: " + a.Name,
		metadata:    newKakaoMessageMetadata(f.ChatID, f.LogID, f.AuthorID, messagetype.Contact, "[contact]", 0),
		transferErr: errContactTransfer,
		download: func(ctx context.Context) (attachmentMedia, error) {
			data, err := media.DownloadContact(ctx, photoHTTPClient, a)
			return attachmentMedia{data: data, name: "contact.vcf", mime: "text/vcard"}, err
		},
	})
}
