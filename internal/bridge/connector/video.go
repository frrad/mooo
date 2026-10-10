package connector

import (
	"context"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

func convertVideo(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, msg events.VideoMessage) (*bridgev2.ConvertedMessage, error) {
	v := msg.Message
	a := v.Attachment
	return convertAttachment(ctx, portal, intent, attachmentSpec{
		kind: "video", failedTo: "displayed", msgType: event.MsgVideo, body: a.Comment,
		metadata:    newKakaoMessageMetadata(v.ChatID, v.LogID, v.AuthorID, messagetype.Video, "[video]", 0),
		transferErr: errPhotoTransfer,
		download: func(ctx context.Context) (attachmentMedia, error) {
			data, err := media.DownloadVideo(ctx, photoHTTPClient, a)
			info := event.FileInfo{Width: int(a.Width), Height: int(a.Height), Duration: int(a.Duration * 1000)}
			return attachmentMedia{data: data, name: "video.mp4", mime: "video/mp4", info: info}, err
		},
	})
}
