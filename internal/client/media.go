package client

import (
	"context"
	"fmt"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/media"
)

// SendImage validates and uploads one JPEG or PNG through the current session.
// SHIP, POST, and the byte stream are each sent exactly once. Transport failure
// is ambiguous and is never retried automatically.
func (s *Session) SendImage(ctx context.Context, chatID int64, data []byte) (media.SendResult, error) {
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
	shipPacket, err := s.Request(ctx, media.ShipCommand, shipBody)
	if err != nil {
		return media.SendResult{}, err
	}
	ship, err := media.DecodeShipResponse(shipPacket.Body)
	if err != nil {
		return media.SendResult{}, err
	}

	dial := s.mediaDial
	if dial == nil {
		dial = dialSecure
	}
	upload, err := dial(ctx, ship.Host, ship.Port)
	if err != nil {
		return media.SendResult{}, fmt.Errorf("client: media connect: %w", err)
	}
	defer func() { _ = upload.close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = upload.c.SetDeadline(deadline)
	} else {
		_ = upload.c.SetDeadline(time.Now().Add(60 * time.Second))
	}
	postBody, err := (media.PostRequest{
		UserID: s.userID, Key: ship.Key, ChatID: chatID, Image: image,
		AppVersion: s.appVersion, MediaID: time.Now().UnixMilli(),
	}).MarshalBSON()
	if err != nil {
		return media.SendResult{}, err
	}
	postPacket, pending, err := upload.request(1, media.PostCommand, postBody)
	if err != nil {
		return media.SendResult{}, fmt.Errorf("client: media POST: %w", err)
	}
	status, err := responseStatus(postPacket)
	if err != nil {
		return media.SendResult{}, ErrProtocol
	}
	if status != 0 {
		return media.SendResult{}, StatusError{Command: media.PostCommand, Status: status}
	}
	offset, err := media.DecodePostOffset(postPacket.Body, len(image.Data))
	if err != nil {
		return media.SendResult{}, err
	}
	if offset < len(image.Data) {
		encrypted, err := upload.secure.Encrypt(image.Data[offset:])
		if err != nil {
			return media.SendResult{}, fmt.Errorf("client: media encrypt: %w", err)
		}
		if err := writeAll(upload.c, encrypted); err != nil {
			return media.SendResult{}, fmt.Errorf("client: media stream: %w", err)
		}
	}
	for _, packet := range pending {
		if packet.Header.Method == media.CompleteCommand {
			return decodeMediaComplete(packet)
		}
	}
	for range requestLimit {
		packet, err := upload.read()
		if err != nil {
			return media.SendResult{}, fmt.Errorf("client: media COMPLETE: %w", err)
		}
		if packet.Header.Method == media.CompleteCommand {
			return decodeMediaComplete(packet)
		}
	}
	return media.SendResult{}, ErrProtocol
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
