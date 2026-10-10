package connector

import (
	"context"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// attachmentSpec describes one inbound Kakao attachment that is downloaded,
// uploaded to Matrix and sent as a single media event.
type attachmentSpec struct {
	// kind names the attachment in the conversion-gap notice ("photo").
	kind string
	// failedTo completes "could not be ..." in that notice ("displayed").
	failedTo string
	// eventType defaults to m.room.message.
	eventType event.Type
	// msgType is left empty for m.sticker events.
	msgType event.MessageType
	// omitFileName leaves the content's filename unset (stickers).
	omitFileName bool
	// body defaults to the uploaded file name.
	body string
	// metadata is stored with the delivered media part.
	metadata *KakaoMessageMetadata
	// gapMetadata is stored with the conversion-gap notice; nil reuses metadata.
	gapMetadata *KakaoMessageMetadata
	// deterministic maps a download error to a conversion-gap category; nil
	// uses deterministicPhotoFailure. Other download errors are transient.
	deterministic func(error) (string, bool)
	// transferErr is the sanitized transient error for download and upload.
	transferErr error
	download    func(context.Context) (attachmentMedia, error)
}

// attachmentMedia is a downloaded attachment ready for Matrix upload. info
// carries type-specific fields; its MIME type and size are filled in from mime
// and data.
type attachmentMedia struct {
	data []byte
	name string
	mime string
	info event.FileInfo
}

// convertAttachment converts one attachment within the shared transfer timeout.
func convertAttachment(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, spec attachmentSpec) (*bridgev2.ConvertedMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, matrixImageTransferTimeout)
	defer cancel()
	part, err := attachmentPart(ctx, portal, intent, spec)
	if err != nil {
		return nil, err
	}
	return &bridgev2.ConvertedMessage{Parts: []*bridgev2.ConvertedMessagePart{part}}, nil
}

// attachmentPart downloads and uploads one attachment and returns its Matrix
// part, or a notice part for a deterministic download failure. The caller owns
// the transfer timeout.
func attachmentPart(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, spec attachmentSpec) (*bridgev2.ConvertedMessagePart, error) {
	downloaded, err := spec.download(ctx)
	if err != nil {
		deterministic := spec.deterministic
		if deterministic == nil {
			deterministic = deterministicPhotoFailure
		}
		category, ok := deterministic(err)
		if !ok {
			return nil, spec.transferErr
		}
		metadata := spec.gapMetadata
		if metadata == nil {
			metadata = spec.metadata
		}
		metadata.ConversionGap = category
		notice := fmt.Sprintf("A KakaoTalk %s could not be %s (%s).", spec.kind, spec.failedTo, category)
		return messageWithMetadata(event.MsgNotice, notice, metadata).Parts[0], nil
	}
	uri, file, err := intent.UploadMedia(ctx, portal.MXID, downloaded.data, downloaded.name, downloaded.mime)
	if err != nil {
		return nil, spec.transferErr
	}
	info := downloaded.info
	info.MimeType = downloaded.mime
	info.Size = len(downloaded.data)
	content := &event.MessageEventContent{MsgType: spec.msgType, Body: spec.body, URL: uri, File: file, Info: &info}
	if content.Body == "" {
		content.Body = downloaded.name
	}
	if !spec.omitFileName {
		content.FileName = downloaded.name
	}
	// bridgev2 returns either a plain URL or encrypted file info; never send
	// both.
	if file != nil {
		content.URL = ""
	}
	eventType := spec.eventType
	if eventType.Type == "" {
		eventType = event.EventMessage
	}
	return &bridgev2.ConvertedMessagePart{Type: eventType, Content: content, DBMetadata: spec.metadata}, nil
}
