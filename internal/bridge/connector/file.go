package connector

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

var errFileTransfer = errors.New("connector: file transfer failed")

func convertFile(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg events.FileMessage) (*bridgev2.ConvertedMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, matrixImageTransferTimeout)
	defer cancel()
	f := msg.Message
	a := f.Attachment
	metadata := newKakaoMessageMetadata(f.ChatID, f.LogID, f.AuthorID, messagetype.File, "[file]", 0)
	data, err := media.DownloadFile(ctx, photoHTTPClient, a)
	if err != nil {
		if category, ok := deterministicPhotoFailure(err); ok {
			metadata.ConversionGap = category
			return messageWithMetadata(event.MsgNotice, fmt.Sprintf("A KakaoTalk file could not be downloaded (%s).", category), metadata), nil
		}
		return nil, errFileTransfer
	}
	mimeType := strings.SplitN(http.DetectContentType(data), ";", 2)[0]
	uri, file, err := intent.UploadMedia(ctx, portal.MXID, data, a.Name, mimeType)
	if err != nil {
		return nil, errFileTransfer
	}
	content := &event.MessageEventContent{MsgType: event.MsgFile, Body: a.Name, FileName: a.Name, URL: uri, File: file, Info: &event.FileInfo{MimeType: mimeType, Size: len(data)}}
	if file != nil {
		content.URL = ""
	}
	return &bridgev2.ConvertedMessage{Parts: []*bridgev2.ConvertedMessagePart{{Type: event.EventMessage, Content: content, DBMetadata: metadata}}}, nil
}
