package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/media"
)

// SendImage validates and uploads one JPEG or PNG through the current session.
// SHIP, POST, and the byte stream are each sent exactly once. Transport failure
// is ambiguous and is never retried automatically.
// SendImage uploads one photo with an optional caption.
func (s *Session) SendImage(ctx context.Context, chatID int64, data []byte, caption string) (media.SendResult, error) {
	if s == nil || ctx == nil {
		return media.SendResult{}, ErrProtocol
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
func (s *Session) SendUpload(ctx context.Context, chatID int64, upload media.Upload) (media.SendResult, error) {
	shipBody, err := (media.UploadShipRequest{ChatID: chatID, Upload: upload}).MarshalBSON()
	if err != nil {
		return media.SendResult{}, err
	}
	if s == nil || ctx == nil {
		return media.SendResult{}, ErrProtocol
	}
	return s.trailerUpload(ctx, shipBody, upload.Data, func(key string) ([]byte, error) {
		return (media.UploadPostRequest{
			UserID: s.userID, Key: key, ChatID: chatID, Upload: upload,
			AppVersion: s.appVersion, MediaID: time.Now().UnixMilli(),
		}).MarshalBSON()
	})
}

func (s *Session) trailerUpload(ctx context.Context, shipBody, data []byte, post func(key string) ([]byte, error)) (media.SendResult, error) {
	shipPacket, err := s.Request(ctx, media.ShipCommand, shipBody)
	if err != nil {
		return media.SendResult{}, err
	}
	ship, err := media.DecodeShipResponse(shipPacket.Body)
	if err != nil {
		return media.SendResult{}, err
	}
	postBody, err := post(ship.Key)
	if err != nil {
		return media.SendResult{}, err
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
func (s *Session) SendAlbum(ctx context.Context, chatID int64, photos [][]byte, caption string) (chat.WriteResponse, error) {
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
	if s == nil || ctx == nil {
		return chat.WriteResponse{}, ErrProtocol
	}
	shipPacket, err := s.Request(ctx, media.AlbumShipCommand, shipBody)
	if err != nil {
		return chat.WriteResponse{}, fmt.Errorf("%w: MSHIP: %w", ErrAlbumNotCreated, err)
	}
	if status, err := responseStatus(shipPacket); err != nil || status != 0 {
		return chat.WriteResponse{}, fmt.Errorf("%w: MSHIP status %d", ErrAlbumNotCreated, status)
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
		if status, err := responseStatus(complete); err != nil || status != 0 {
			return chat.WriteResponse{}, fmt.Errorf("%w: photo %d COMPLETE status %d", ErrAlbumNotCreated, i+1, status)
		}
	}
	extra, err := media.AlbumWriteExtra(images, ship, caption)
	if err != nil {
		return chat.WriteResponse{}, fmt.Errorf("%w: %w", ErrAlbumNotCreated, err)
	}
	body, err := (chat.WriteRequest{ChatID: chatID, Type: media.MultiPhotoType, Extra: extra}).MarshalBSON()
	if err != nil {
		return chat.WriteResponse{}, fmt.Errorf("%w: %w", ErrAlbumNotCreated, err)
	}
	reply, err := s.Request(ctx, chat.WriteCommand, body)
	if err != nil {
		return chat.WriteResponse{}, err
	}
	return chat.DecodeWriteResponse(reply.Body)
}

// uploadToMediaServer sends one POST or MPOST on a new dedicated media
// connection, streams the bytes from the server's offset and returns the
// server's COMPLETE packet.
func (s *Session) uploadToMediaServer(ctx context.Context, host string, port int, command string, postBody, data []byte) (loco.Packet, error) {
	dial := s.mediaDial
	if dial == nil {
		dial = dialSecure
	}
	upload, err := dial(ctx, host, port)
	if err != nil {
		return loco.Packet{}, fmt.Errorf("client: media connect: %w", err)
	}
	defer func() { _ = upload.close() }()
	// Cancellation closes the dedicated media connection, interrupting a
	// blocked write or a wait for COMPLETE. The outcome remains ambiguous.
	stopCancel := context.AfterFunc(ctx, func() { _ = upload.close() })
	defer stopCancel()
	if deadline, ok := ctx.Deadline(); ok {
		_ = upload.c.SetDeadline(deadline)
	} else {
		_ = upload.c.SetDeadline(time.Now().Add(60 * time.Second))
	}
	postPacket, pending, err := upload.request(1, command, postBody)
	if err != nil {
		return loco.Packet{}, fmt.Errorf("client: media %s: %w", command, err)
	}
	status, err := responseStatus(postPacket)
	if err != nil {
		return loco.Packet{}, ErrProtocol
	}
	if status != 0 {
		return loco.Packet{}, StatusError{Command: command, Status: status}
	}
	offset, err := media.DecodePostOffset(postPacket.Body, len(data))
	if err != nil {
		return loco.Packet{}, err
	}
	if offset < len(data) {
		encrypted, err := upload.secure.Encrypt(data[offset:])
		if err != nil {
			return loco.Packet{}, fmt.Errorf("client: media encrypt: %w", err)
		}
		if err := writeAll(upload.c, encrypted); err != nil {
			return loco.Packet{}, fmt.Errorf("client: media stream: %w", err)
		}
	}
	for _, packet := range pending {
		if packet.Header.Method == media.CompleteCommand {
			return packet, nil
		}
	}
	for range requestLimit {
		packet, err := upload.read()
		if err != nil {
			return loco.Packet{}, fmt.Errorf("client: media COMPLETE: %w", err)
		}
		if packet.Header.Method == media.CompleteCommand {
			return packet, nil
		}
	}
	return loco.Packet{}, ErrProtocol
}

func decodeMediaComplete(packet loco.Packet) (media.SendResult, error) {
	status, err := responseStatus(packet)
	if err != nil {
		return media.SendResult{}, ErrProtocol
	}
	if status != 0 {
		return media.SendResult{}, StatusError{Command: media.CompleteCommand, Status: status}
	}
	return media.DecodeComplete(packet.Body)
}

func writeAll(conn interface{ Write([]byte) (int, error) }, data []byte) error {
	_, err := writeAllCount(conn, data)
	return err
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
