package connector

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	bridgematrix "maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
)

const matrixImageTransferTimeout = 30 * time.Second

var matrixImageDownloader = downloadMatrixImageBounded

const matrixUploadTransferTimeout = 3 * time.Minute

// kakaoUploadTimeout caps sending a file or video to KakaoTalk after it has
// been fetched from Matrix. The client bounds each stage and fails a stalled
// stream; this backstop only has to outlast a slow but progressing upload,
// so it grows with the size at a 64 KiB/s floor. A video sent over the HTTP
// upload may also wait up to 10 minutes for the server to process it.
func kakaoUploadTimeout(upload media.Upload) time.Duration {
	timeout := 2*time.Minute + time.Duration(len(upload.Data)/(64<<10))*time.Second
	if upload.Type == messagetype.Video {
		timeout += 10 * time.Minute
	}
	return timeout
}

var matrixUploadDownloader = func(ctx context.Context, intent bridgev2.MatrixAPI, uri id.ContentURIString, fileInfo *event.EncryptedFileInfo) ([]byte, error) {
	return downloadMatrixMediaBounded(ctx, intent, uri, fileInfo, media.MaxUploadBytes)
}

var (
	errUnsupportedImageReply  = errors.New("connector: image replies are not supported")
	errUnsupportedUploadReply = errors.New("connector: KakaoTalk has no file, video or album reply")
)

// KakaoTalk offers no photo, file, video or album attachment while replying.
var (
	imageReplyRefusal  = certainRefusal(errUnsupportedImageReply, "KakaoTalk cannot send a photo as a reply; the photo was not sent. Send it without the reply.")
	uploadReplyRefusal = certainRefusal(errUnsupportedUploadReply, "KakaoTalk cannot send a file, video or album as a reply; it was not sent. Send it without the reply.")
)

// mediaSend fetches one Matrix media event through intent and sends it to
// chatID. begin reserves the event's single source send.
type mediaSend func(ctx context.Context, c outboundAPI, intent bridgev2.MatrixAPI, chatID int64, content *event.MessageEventContent, begin func(context.Context) error) (*bridgev2.MatrixMessageResponse, error)

// withMediaSend runs the checks every Matrix media send shares, in order: a
// reply is refused with replyRefusal, rejected (when not nil) refuses media
// known to be invalid before any fetch, and the user's Matrix intent is
// looked up to fetch the media.
func (kc *KakaoClient) withMediaSend(ctx context.Context, msg *bridgev2.MatrixMessage, c outboundAPI, chatID int64, replyRefusal, rejected error, send mediaSend) (*bridgev2.MatrixMessageResponse, error) {
	if msg.ReplyTo != nil || msg.Content.RelatesTo != nil && msg.Content.RelatesTo.GetReplyTo() != "" {
		return nil, replyRefusal
	}
	if msg.Portal.Bridge == nil {
		return nil, bridgev2.ErrFailedToGetIntent
	}
	if rejected != nil {
		return nil, rejected
	}
	intent, ok := msg.Portal.GetIntentFor(ctx, kc.selfSender(), kc.login, bridgev2.RemoteEventMessage)
	if !ok {
		return nil, bridgev2.ErrFailedToGetIntent
	}
	return send(ctx, c, intent, chatID, msg.Content, func(ctx context.Context) error {
		return kc.beginOutbound(ctx, msg)
	})
}

// oversizedImage refuses an image whose declared size KakaoTalk cannot
// accept, before it is fetched.
func oversizedImage(content *event.MessageEventContent) error {
	if content.Info != nil && content.Info.Size > media.MaxImageBytes {
		return media.ErrInvalidImage
	}
	return nil
}

// outboundCtx bounds one outbound media transfer by timeout and by the
// current connection, so Disconnect interrupts it. The caller must call
// release when the transfer returns.
func (kc *KakaoClient) outboundCtx(ctx context.Context, timeout time.Duration) (_ context.Context, release func()) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	lifecycle := kc.connectionLifecycle()
	if lifecycle == nil {
		return ctx, cancel
	}
	ctx, releaseLifecycle := withConnectionLifecycle(context.WithValue(ctx, connectionLifecycleKey{}, lifecycle))
	return ctx, func() {
		releaseLifecycle()
		cancel()
	}
}

// sendMatrixImage fetches and validates the Matrix image before begin
// reserves the event's single source send, so a failed fetch or an image
// KakaoTalk cannot accept never consumes the reservation or reaches Kakao.
// Disconnect interrupts the transfer through the connection lifecycle.
func (kc *KakaoClient) sendMatrixImage(ctx context.Context, c outboundAPI, intent bridgev2.MatrixAPI, chatID int64, content *event.MessageEventContent, begin func(context.Context) error) (*bridgev2.MatrixMessageResponse, error) {
	ctx, release := kc.outboundCtx(ctx, matrixImageTransferTimeout)
	defer release()
	// A body that differs from the filename is the image's caption.
	caption := ""
	if content.FileName != "" && content.Body != content.FileName {
		caption = content.Body
	}
	if !media.ValidCaption(caption) {
		return nil, certainRefusal(media.ErrInvalidCaption, "The image caption is too long for KakaoTalk; the image was not sent.")
	}
	data, err := matrixImageDownloader(ctx, intent, content.URL, content.File)
	if err != nil {
		return nil, retriableFetchFailure(errors.New("download Matrix image failed"), "The image could not be fetched from Matrix, so it was not sent to KakaoTalk.")
	}
	if _, err := media.PrepareImage(data); err != nil {
		return nil, certainRefusal(err, "KakaoTalk accepts only JPEG and PNG photos up to 16 MiB; this image was not sent.")
	}
	if err := begin(ctx); err != nil {
		return nil, err
	}
	response, err := c.SendImage(ctx, chatID, data, caption)
	if err != nil {
		kc.noteMediaReplyFailure(err)
		return nil, outboundSendError(err)
	}
	logID, sendAt, err := media.SendResultPosition(response)
	if err != nil {
		return nil, outboundAcceptedWithoutPosition(err)
	}
	return kc.sentMessage(chatID, logID, sendAt, messagetype.Photo, "[image]"), nil
}

// sendMatrixUpload sends one Matrix file, video or audio as a KakaoTalk file
// or video. Name, type and size are checked before the download, the bytes
// are validated before the durable reservation, and the upload runs once.
func (kc *KakaoClient) sendMatrixUpload(ctx context.Context, c outboundAPI, intent bridgev2.MatrixAPI, chatID int64, content *event.MessageEventContent, begin func(context.Context) error) (*bridgev2.MatrixMessageResponse, error) {
	fetchCtx, releaseFetch := kc.outboundCtx(ctx, matrixUploadTransferTimeout)
	defer releaseFetch()
	name, caption := content.FileName, ""
	if name == "" {
		name = content.Body
	} else if content.Body != name {
		caption = content.Body
	}
	if content.Info != nil && content.Info.Size > media.MaxUploadBytes {
		return nil, uploadRejectedStatus(media.ErrUploadTooLarge)
	}
	if _, err := media.ClassifyUpload(name, caption); err != nil {
		return nil, uploadRejectedStatus(err)
	}
	data, err := matrixUploadDownloader(fetchCtx, intent, content.URL, content.File)
	if err != nil {
		return nil, retriableFetchFailure(errors.New("download Matrix file failed"), "The file could not be fetched from Matrix, so it was not sent to KakaoTalk.")
	}
	upload, err := media.PrepareUpload(name, data, caption)
	if err != nil {
		return nil, uploadRejectedStatus(err)
	}
	ctx, release := kc.outboundCtx(ctx, kakaoUploadTimeout(upload))
	defer release()
	if err := begin(ctx); err != nil {
		return nil, err
	}
	response, err := c.SendUpload(ctx, chatID, upload)
	if err != nil {
		kc.noteMediaReplyFailure(err)
		return nil, outboundSendError(err)
	}
	logID, sendAt, err := media.SendResultPosition(response)
	if err != nil {
		return nil, outboundAcceptedWithoutPosition(err)
	}
	return kc.sentMessage(chatID, logID, sendAt, upload.Type, upload.Name), nil
}

// mautrix v0.32 removed the legacy Beeper gallery types. Keep their wire
// format locally so existing clients can still send KakaoTalk albums.
const msgBeeperGallery event.MessageType = "com.beeper.gallery"

type matrixGalleryContent struct {
	Images  []*event.MessageEventContent `json:"com.beeper.gallery.images"`
	Caption string                       `json:"com.beeper.gallery.caption"`
}

func (kc *KakaoClient) sendMatrixGallery(ctx context.Context, c outboundAPI, intent bridgev2.MatrixAPI, chatID int64, evt *event.Event, begin func(context.Context) error) (*bridgev2.MatrixMessageResponse, error) {
	if evt == nil {
		return nil, certainRefusal(media.ErrInvalidAlbum, "The album contains invalid data; it was not sent.")
	}
	data, err := evt.Content.MarshalJSON()
	var gallery matrixGalleryContent
	if err != nil || json.Unmarshal(data, &gallery) != nil {
		return nil, certainRefusal(media.ErrInvalidAlbum, "The album contains invalid data; it was not sent.")
	}
	return kc.sendMatrixAlbum(ctx, c, intent, chatID, &gallery, begin)
}

// sendMatrixAlbum sends one Matrix gallery as one KakaoTalk album. Like the
// Mac client, a single photo is sent as an ordinary photo. Every photo is
// fetched and validated before the durable reservation, and the album is
// sent once.
func (kc *KakaoClient) sendMatrixAlbum(ctx context.Context, c outboundAPI, intent bridgev2.MatrixAPI, chatID int64, content *matrixGalleryContent, begin func(context.Context) error) (*bridgev2.MatrixMessageResponse, error) {
	images, caption := content.Images, content.Caption
	if len(images) == 1 && images[0] != nil {
		single := *images[0]
		if caption != "" {
			single.FileName, single.Body = single.Body, caption
		}
		return kc.sendMatrixImage(ctx, c, intent, chatID, &single, begin)
	}
	if len(images) < media.MinAlbumPhotos || len(images) > media.MaxAlbumPhotos {
		return nil, certainRefusal(media.ErrInvalidAlbum, "KakaoTalk albums hold 2 to 30 photos; the album was not sent.")
	}
	if !media.ValidCaption(caption) {
		return nil, certainRefusal(media.ErrInvalidCaption, "The album caption is too long for KakaoTalk; the album was not sent.")
	}
	ctx, release := kc.outboundCtx(ctx, matrixUploadTransferTimeout)
	defer release()
	photos := make([][]byte, 0, len(images))
	total := 0
	for _, image := range images {
		if image == nil {
			return nil, certainRefusal(media.ErrInvalidAlbum, "The album contains an invalid photo; it was not sent.")
		}
		data, err := matrixImageDownloader(ctx, intent, image.URL, image.File)
		if err != nil {
			return nil, retriableFetchFailure(errors.New("download Matrix album photo failed"), "A photo could not be fetched from Matrix, so the album was not sent to KakaoTalk.")
		}
		if _, err := media.PrepareImage(data); err != nil {
			return nil, certainRefusal(err, "KakaoTalk albums accept only JPEG and PNG photos up to 16 MiB; the album was not sent.")
		}
		total += len(data)
		if total > media.MaxAlbumBytes {
			return nil, certainRefusal(media.ErrInvalidAlbum, "Albums sent to KakaoTalk from Matrix are limited to 64 MiB in total; the album was not sent.")
		}
		photos = append(photos, data)
	}
	if err := begin(ctx); err != nil {
		return nil, err
	}
	response, err := c.SendAlbum(ctx, chatID, photos, caption)
	kc.noteMediaReplyFailure(err)
	if errors.Is(err, client.ErrAlbumNotCreated) {
		// No WRITE was sent, so no message exists. The reservation keeps this
		// Matrix event from being sent again.
		return nil, certainSendFailure(err, "The album could not be uploaded to KakaoTalk; no message was created.")
	}
	if err != nil {
		return nil, outboundSendError(err)
	}
	if response.LogID <= 0 {
		return nil, outboundAcceptedWithoutPosition(errors.New("KakaoTalk accepted the album without a log ID"))
	}
	return kc.sentMessage(chatID, response.LogID, response.SendAt, messagetype.MultiPhoto, "[album]"), nil
}

// uploadRejectedStatus reports a file KakaoTalk would not accept. Nothing was
// sent, so the refusal is certain.
func uploadRejectedStatus(err error) error {
	message := "The file name is not valid for KakaoTalk; the file was not sent."
	switch {
	case errors.Is(err, media.ErrUploadTooLarge):
		message = "Files sent to KakaoTalk from Matrix are limited to 64 MiB; the file was not sent."
	case errors.Is(err, media.ErrDeniedExtension):
		message = "KakaoTalk does not allow this file type; the file was not sent."
	case errors.Is(err, media.ErrCaptionNotSupported):
		message = "KakaoTalk files cannot carry a caption; the file was not sent. Send the file and the text separately."
	case errors.Is(err, media.ErrInvalidCaption):
		message = "The video caption is too long for KakaoTalk; the video was not sent."
	}
	return certainRefusal(err, message)
}

func downloadMatrixImageBounded(ctx context.Context, intent bridgev2.MatrixAPI, uri id.ContentURIString, fileInfo *event.EncryptedFileInfo) ([]byte, error) {
	return downloadMatrixMediaBounded(ctx, intent, uri, fileInfo, media.MaxImageBytes)
}

func downloadMatrixMediaBounded(ctx context.Context, intent bridgev2.MatrixAPI, uri id.ContentURIString, fileInfo *event.EncryptedFileInfo, limit int64) ([]byte, error) {
	asIntent, ok := intent.(*bridgematrix.ASIntent)
	if !ok || asIntent == nil || asIntent.Matrix == nil {
		return nil, bridgev2.ErrFailedToGetIntent
	}
	if fileInfo != nil {
		uri = fileInfo.URL
		if err := fileInfo.PrepareForDecryption(); err != nil {
			return nil, media.ErrInvalidImage
		}
	}
	parsed, err := uri.Parse()
	if err != nil {
		return nil, media.ErrInvalidImage
	}
	resp, err := asIntent.Matrix.Download(ctx, parsed)
	if err != nil {
		return nil, media.ErrInvalidImage
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.ContentLength > limit {
		return nil, media.ErrInvalidImage
	}
	reader := io.Reader(resp.Body)
	var closeReader io.Closer = resp.Body
	if fileInfo != nil {
		decryptReader := fileInfo.DecryptStream(resp.Body)
		reader = decryptReader
		closeReader = decryptReader
	}
	return readBoundedMatrixMedia(reader, closeReader, limit)
}

func readBoundedMatrixMedia(reader io.Reader, closer io.Closer, limit int64) ([]byte, error) {
	data, readErr := io.ReadAll(io.LimitReader(reader, limit+1))
	closeErr := closer.Close()
	if readErr != nil || closeErr != nil || int64(len(data)) > limit {
		return nil, media.ErrInvalidImage
	}
	return data, nil
}
