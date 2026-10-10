package connector

import (
	"context"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

func convertAudio(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg events.AudioMessage) (*bridgev2.ConvertedMessage, error) {
	v := msg.Message
	a := v.Attachment
	return convertAttachment(ctx, portal, intent, attachmentSpec{
		kind: "audio", failedTo: "displayed", msgType: event.MsgAudio,
		metadata:    newKakaoMessageMetadata(v.ChatID, v.LogID, v.AuthorID, messagetype.Audio, "[audio]", 0),
		transferErr: errPhotoTransfer,
		download: func(ctx context.Context) (attachmentMedia, error) {
			data, err := media.DownloadAudio(ctx, photoHTTPClient, a)
			return attachmentMedia{data: data, name: "audio.m4a", mime: "audio/mp4", info: event.FileInfo{Duration: int(a.Duration)}}, err
		},
	})
}
