package connector

import (
	"context"
	"fmt"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

func convertAudio(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg events.AudioMessage) (*bridgev2.ConvertedMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, matrixImageTransferTimeout)
	defer cancel()
	v := msg.Message
	a := v.Attachment
	metadata := newKakaoMessageMetadata(v.ChatID, v.LogID, v.AuthorID, messagetype.Audio, "[audio]", 0)
	data, err := media.DownloadAudio(ctx, photoHTTPClient, a)
	if err != nil {
		if category, ok := deterministicPhotoFailure(err); ok {
			metadata.ConversionGap = category
			return messageWithMetadata(event.MsgNotice, fmt.Sprintf("A KakaoTalk audio could not be displayed (%s).", category), metadata), nil
		}
		return nil, errPhotoTransfer
	}
	uri, file, err := intent.UploadMedia(ctx, portal.MXID, data, "audio.m4a", "audio/mp4")
	if err != nil {
		return nil, errPhotoTransfer
	}
	content := &event.MessageEventContent{MsgType: event.MsgAudio, Body: "audio.m4a", FileName: "audio.m4a", URL: uri, File: file, Info: &event.FileInfo{MimeType: "audio/mp4", Size: len(data), Duration: int(a.Duration)}}
	if file != nil {
		content.URL = ""
	}
	return &bridgev2.ConvertedMessage{Parts: []*bridgev2.ConvertedMessagePart{{Type: event.EventMessage, Content: content, DBMetadata: metadata}}}, nil
}
