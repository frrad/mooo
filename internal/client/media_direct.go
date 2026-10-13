package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/frrad/mooo/internal/protocol/media"
)

// ErrPhotoNotCreated marks a failed alternate upload before WRITEMEDIA was
// attempted. Bytes may have reached storage, but no chat message was created.
var ErrPhotoNotCreated = errors.New("client: photo message was not created")

func (c *Client) directPhoto(ctx context.Context, s *session, chatID int64, data []byte, caption string) (media.SendResult, error) {
	fail := func(err error) (media.SendResult, error) {
		return media.SendResult{}, fmt.Errorf("%w: %w", ErrPhotoNotCreated, err)
	}
	doer, state, err := c.httpProfile(ctx)
	if err != nil {
		return fail(err)
	}
	profile, err := webProfile(state)
	if err != nil {
		return fail(err)
	}
	image, err := media.PrepareImage(data)
	if err != nil {
		return fail(err)
	}
	ticket, err := media.PrepareDirectPhoto(ctx, doer, profile, chatID, image)
	if err != nil {
		return fail(err)
	}
	body, err := (media.DirectPhotoWriteRequest{ChatID: chatID, MessageID: time.Now().UnixMilli(), Image: image, Ticket: ticket, Caption: caption}).MarshalBSON()
	if err != nil {
		return fail(err)
	}
	if err = media.UploadDirectPhoto(ctx, doer, profile, ticket, image); err != nil {
		return fail(err)
	}
	// Once this request is attempted, transport/decoding failure is ambiguous.
	// Neither upload nor WRITEMEDIA is retried automatically.
	reply, err := s.request(ctx, media.WriteMediaCommand, body)
	if err != nil {
		return media.SendResult{}, err
	}
	return media.DecodeDirectPhotoWrite(reply.Body, chatID)
}
