package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
)

// SendImage validates and uploads one JPEG or PNG through the current session.
// SHIP, POST, and the byte stream are each sent exactly once. Transport failure
// is ambiguous and is never retried automatically.
func (s *session) SendImage(ctx context.Context, chatID int64, data []byte, caption string) (media.SendResult, error) {
	if !media.ValidCaption(caption) {
		return media.SendResult{}, media.ErrInvalidCaption
	}
	image, err := media.PrepareImage(data)
	if err != nil {
		return media.SendResult{}, err
	}
	shipBody, err := (media.ShipRequest{ChatID: chatID, Image: image}).MarshalBSON()
	if err != nil {
		return media.SendResult{}, err
	}
	return s.trailerUpload(ctx, shipBody, image.Data, func(key string) ([]byte, error) {
		return (media.PostRequest{
			UserID: s.userID, Key: key, ChatID: chatID, Image: image, Comment: caption,
			AppVersion: s.appVersion, MediaID: time.Now().UnixMilli(),
		}).MarshalBSON()
	})
}

// SendUpload uploads one prepared file or video with the same single-attempt
// SHIP, POST, stream and COMPLETE sequence as a photo.
func (s *session) SendUpload(ctx context.Context, chatID int64, upload media.Upload) (media.SendResult, error) {
	shipBody, err := (media.UploadShipRequest{ChatID: chatID, Upload: upload}).MarshalBSON()
	if err != nil {
		return media.SendResult{}, err
	}
	return s.trailerUpload(ctx, shipBody, upload.Data, func(key string) ([]byte, error) {
		return (media.UploadPostRequest{
			UserID: s.userID, Key: key, ChatID: chatID, Upload: upload,
			AppVersion: s.appVersion, MediaID: time.Now().UnixMilli(),
		}).MarshalBSON()
	})
}

func (s *session) trailerUpload(ctx context.Context, shipBody, data []byte, post func(key string) ([]byte, error)) (media.SendResult, error) {
	notSent := func(err error) (media.SendResult, error) {
		return media.SendResult{}, fmt.Errorf("%w: %w", ErrUploadNotSent, err)
	}
	shipCtx, cancel := context.WithTimeout(ctx, mediaReplyTimeout)
	shipPacket, err := s.request(shipCtx, media.ShipCommand, shipBody)
	cancel()
	if err != nil {
		return notSent(err)
	}
	ship, err := media.DecodeShipResponse(shipPacket.Body)
	if err != nil {
		return notSent(err)
	}
	if ship.Redirect {
		return notSent(media.ErrUploadRedirect)
	}
	postBody, err := post(ship.Key)
	if err != nil {
		return notSent(err)
	}
	complete, err := s.uploadToMediaServer(ctx, ship.Host, ship.Port, media.PostCommand, postBody, data)
	if err != nil {
		return media.SendResult{}, err
	}
	return decodeMediaComplete(complete)
}

// ErrAlbumNotCreated reports an album that failed before its WRITE. Photos may
// have been uploaded, but no message was created in the chat.
var ErrAlbumNotCreated = errors.New("client: album message was not created")

// SendAlbum sends 2 to 30 JPEG or PNG photos as one album: one MSHIP, one
// MPOST and stream per photo on its own media connection, then one type-27
// WRITE. No stage is retried; a failure before the WRITE is ErrAlbumNotCreated.
func (s *session) SendAlbum(ctx context.Context, chatID int64, photos [][]byte, caption string) (chat.WriteResponse, error) {
	images := make([]media.Image, 0, len(photos))
	for _, data := range photos {
		image, err := media.PrepareImage(data)
		if err != nil {
			return chat.WriteResponse{}, err
		}
		images = append(images, image)
	}
	shipBody, err := (media.AlbumShipRequest{ChatID: chatID, Images: images}).MarshalBSON()
	if err != nil {
		return chat.WriteResponse{}, err
	}
	if !media.ValidCaption(caption) {
		return chat.WriteResponse{}, media.ErrInvalidCaption
	}
	shipPacket, err := s.request(ctx, media.AlbumShipCommand, shipBody)
	if err != nil {
		return chat.WriteResponse{}, fmt.Errorf("%w: MSHIP: %w", ErrAlbumNotCreated, err)
	}
	ship, err := media.DecodeAlbumShipResponse(shipPacket.Body, len(images))
	if err != nil {
		return chat.WriteResponse{}, fmt.Errorf("%w: %w", ErrAlbumNotCreated, err)
	}
	for i, image := range images {
		postBody, err := (media.AlbumPostRequest{UserID: s.userID, Key: ship.Keys[i], Image: image, AppVersion: s.appVersion}).MarshalBSON()
		if err != nil {
			return chat.WriteResponse{}, fmt.Errorf("%w: %w", ErrAlbumNotCreated, err)
		}
		complete, err := s.uploadToMediaServer(ctx, ship.Hosts[i], ship.Ports[i], media.AlbumPostCommand, postBody, image.Data)
		if err != nil {
			return chat.WriteResponse{}, fmt.Errorf("%w: photo %d: %w", ErrAlbumNotCreated, i+1, err)
		}
		if status, err := loco.Status(complete); err != nil || status != 0 {
			return chat.WriteResponse{}, fmt.Errorf("%w: photo %d COMPLETE status %d", ErrAlbumNotCreated, i+1, status)
		}
	}
	extra, err := media.AlbumWriteExtra(images, ship, caption)
	if err != nil {
		return chat.WriteResponse{}, fmt.Errorf("%w: %w", ErrAlbumNotCreated, err)
	}
	body, err := (chat.WriteRequest{ChatID: chatID, Type: messagetype.MultiPhoto, Extra: extra}).MarshalBSON()
	if err != nil {
		return chat.WriteResponse{}, fmt.Errorf("%w: %w", ErrAlbumNotCreated, err)
	}
	reply, err := s.request(ctx, chat.WriteCommand, body)
	if err != nil {
		return chat.WriteResponse{}, err
	}
	return chat.DecodeWriteResponse(reply.Body)
}

// ErrUploadNotSent marks a media upload that failed before any of its bytes
// reached the media server. The server creates the chat message only after
// the whole stream and COMPLETE, so no message exists.
var ErrUploadNotSent = errors.New("client: media upload failed before any bytes were sent")

// mediaStreamChunk is the plaintext size of each secure frame in an upload
// stream, as the Mac client sends it (research/media-upload-stream.md).
const mediaStreamChunk = 4096

var (
	// mediaReplyTimeout bounds the SHIP and POST replies, like the Mac's
	// receive-header timer.
	mediaReplyTimeout = 20 * time.Second
	// mediaStallTimeout bounds one stream frame's write, like the Mac's
	// out-segment watchdog: a stalled upload fails, a slow one does not.
	mediaStallTimeout = 10 * time.Second
	// mediaCompleteTimeout bounds the wait for COMPLETE after the last byte.
	// The Mac waits without a timer; a bridge needs an answer, and a timeout
	// here is ambiguous.
	mediaCompleteTimeout = 60 * time.Second
)

// uploadToMediaServer sends one POST or MPOST on a new dedicated media
// connection, streams the bytes from the server's offset in mediaStreamChunk
// frames and returns the server's COMPLETE packet. A failure before any byte
// is streamed wraps ErrUploadNotSent. The caller's context deadline, when
// earlier, caps every stage.
func (s *session) uploadToMediaServer(ctx context.Context, host string, port int, command string, postBody, data []byte) (loco.Packet, error) {
	notSent := func(err error) (loco.Packet, error) {
		return loco.Packet{}, fmt.Errorf("%w: %w", ErrUploadNotSent, err)
	}
	within := func(timeout time.Duration) time.Time {
		deadline := time.Now().Add(timeout)
		if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
			return ctxDeadline
		}
		return deadline
	}
	dial := s.mediaDial
	if dial == nil {
		dial = dialSecure
	}
	upload, err := dial(ctx, host, port)
	if err != nil {
		return notSent(fmt.Errorf("client: media connect: %w", err))
	}
	defer func() { _ = upload.close() }()
	// Cancellation closes the dedicated media connection, interrupting a
	// blocked write or a wait for COMPLETE. The outcome remains ambiguous
	// once bytes have been streamed.
	stopCancel := context.AfterFunc(ctx, func() { _ = upload.close() })
	defer stopCancel()
	_ = upload.c.SetDeadline(within(mediaReplyTimeout))
	postPacket, pending, err := upload.request(1, command, postBody)
	if err != nil {
		return notSent(fmt.Errorf("client: media %s: %w", command, err))
	}
	status, err := loco.Status(postPacket)
	if err != nil {
		return notSent(protocolErr(command, "status"))
	}
	if status != 0 {
		return notSent(StatusError{Command: command, Status: status})
	}
	offset, err := media.DecodePostOffset(postPacket.Body, len(data))
	if err != nil {
		return notSent(err)
	}
	// An offset at the end streams nothing; the server may still complete
	// the upload, so later failures are ambiguous.
	for rest, streamed := data[offset:], false; len(rest) > 0; {
		n := min(len(rest), mediaStreamChunk)
		encrypted, err := upload.secure.Encrypt(rest[:n])
		if err != nil {
			err = fmt.Errorf("client: media encrypt: %w", err)
			if !streamed {
				return notSent(err)
			}
			return loco.Packet{}, err
		}
		_ = upload.c.SetDeadline(within(mediaStallTimeout))
		written, err := writeAllCount(upload.c, encrypted)
		if err != nil {
			err = fmt.Errorf("client: media stream: %w", err)
			if !streamed && written == 0 {
				return notSent(err)
			}
			return loco.Packet{}, err
		}
		streamed = true
		rest = rest[n:]
	}
	for _, packet := range pending {
		if packet.Header.Method == media.CompleteCommand {
			return packet, nil
		}
	}
	_ = upload.c.SetDeadline(within(mediaCompleteTimeout))
	for range requestLimit {
		packet, err := upload.read()
		if err != nil {
			return loco.Packet{}, fmt.Errorf("client: media COMPLETE: %w", err)
		}
		if packet.Header.Method == media.CompleteCommand {
			return packet, nil
		}
	}
	return loco.Packet{}, protocolErr(media.CompleteCommand, "reply")
}

func decodeMediaComplete(packet loco.Packet) (media.SendResult, error) {
	status, err := loco.Status(packet)
	if err != nil {
		return media.SendResult{}, protocolErr(media.CompleteCommand, "status")
	}
	if status != 0 {
		return media.SendResult{}, StatusError{Command: media.CompleteCommand, Status: status}
	}
	return media.DecodeComplete(packet.Body)
}

func writeAllCount(conn interface{ Write([]byte) (int, error) }, data []byte) (int, error) {
	written := 0
	for len(data) > 0 {
		n, err := conn.Write(data)
		written += n
		if err != nil {
			return written, err
		}
		if n <= 0 {
			return written, fmt.Errorf("zero-byte write")
		}
		data = data[n:]
	}
	return written, nil
}
