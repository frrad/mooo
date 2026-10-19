package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/macweb"
	"github.com/frrad/mooo/internal/protocol/media"
)

// ErrPhotoNotCreated marks a failed alternate upload before WRITEMEDIA was
// attempted. Bytes may have reached storage, but no chat message was created.
var ErrPhotoNotCreated = errors.New("client: photo message was not created")

// directReadyMaxWait caps the ready poll when the server asks for longer; it
// is the Mac client's default maxWaitSeconds.
const directReadyMaxWait = 600 * time.Second

func (c *Client) directPhoto(ctx context.Context, s *session, chatID int64, data []byte, caption string) (media.SendResult, error) {
	fail := func(err error) (media.SendResult, error) {
		return media.SendResult{}, fmt.Errorf("%w: %w", ErrPhotoNotCreated, err)
	}
	image, err := media.PrepareImage(data)
	if err != nil {
		return fail(err)
	}
	return c.directSend(ctx, s, chatID, media.ImageDirectMedia(image), fail, func(ticket media.DirectTicket) ([]byte, error) {
		return (media.DirectPhotoWriteRequest{ChatID: chatID, MessageID: time.Now().UnixMilli(), Image: image, Ticket: ticket, Caption: caption}).MarshalBSON()
	})
}

// directUpload sends a file or video over the HTTP upload a SHIP redirect
// selects. Any failure before WRITEMEDIA wraps ErrUploadNotSent.
func (c *Client) directUpload(ctx context.Context, s *session, chatID int64, upload media.Upload) (media.SendResult, error) {
	fail := func(err error) (media.SendResult, error) {
		return media.SendResult{}, fmt.Errorf("%w: %w", ErrUploadNotSent, err)
	}
	m, err := media.UploadDirectMedia(upload)
	if err != nil {
		return fail(err)
	}
	return c.directSend(ctx, s, chatID, m, fail, func(ticket media.DirectTicket) ([]byte, error) {
		return (media.DirectUploadWriteRequest{ChatID: chatID, MessageID: time.Now().UnixMilli(), Upload: upload, Ticket: ticket}).MarshalBSON()
	})
}

// directSend runs prepare, the chunked upload, the ready poll when prepare
// asks for one, and one WRITEMEDIA. fail marks a failure before WRITEMEDIA,
// which certainly created no message; after WRITEMEDIA is attempted a
// failure is ambiguous. Nothing is retried.
func (c *Client) directSend(ctx context.Context, s *session, chatID int64, m media.DirectMedia, fail func(error) (media.SendResult, error), write func(media.DirectTicket) ([]byte, error)) (media.SendResult, error) {
	// Only the authorized steps can be refused with HTTP 401.
	var doer macweb.Doer
	var profile macweb.Profile
	ticket, err := webCall(c, ctx, func(d macweb.Doer, state authstate.State) (media.DirectTicket, error) {
		p, err := webProfile(state)
		if err != nil {
			return media.DirectTicket{}, err
		}
		doer, profile = d, p
		return media.PrepareDirect(ctx, d, p, chatID, m)
	})
	if err != nil {
		return fail(err)
	}
	body, err := write(ticket)
	if err != nil {
		return fail(err)
	}
	if err = media.UploadDirect(ctx, doer, profile, ticket, m); err != nil {
		return fail(err)
	}
	if ticket.NeedReady {
		if err = c.waitDirectReady(ctx, chatID, m.MediaType, ticket); err != nil {
			return fail(err)
		}
	}
	// Once this request is attempted, transport/decoding failure is ambiguous.
	// Neither upload nor WRITEMEDIA is retried automatically.
	reply, err := s.request(ctx, media.WriteMediaCommand, body)
	if err != nil {
		return media.SendResult{}, err
	}
	return media.DecodeDirectWrite(reply.Body, chatID)
}

// waitDirectReady polls until the server reports the uploaded media ready,
// sleeping the interval it returns, for at most its maxWaitSeconds (capped at
// directReadyMaxWait) and the caller's context.
func (c *Client) waitDirectReady(ctx context.Context, chatID int64, mediaType string, ticket media.DirectTicket) error {
	started := time.Now()
	maxWait := directReadyMaxWait
	prevStatus, jobID := 0, ""
	for {
		ready, err := webCall(c, ctx, func(d macweb.Doer, state authstate.State) (media.DirectReady, error) {
			p, err := webProfile(state)
			if err != nil {
				return media.DirectReady{}, err
			}
			return media.CheckDirectReady(ctx, d, p, chatID, mediaType, ticket, prevStatus, jobID)
		})
		if err != nil {
			return err
		}
		if ready.Status == media.DirectReadyDone {
			return nil
		}
		if ready.MaxWaitSeconds > 0 {
			maxWait = min(time.Duration(ready.MaxWaitSeconds)*time.Second, directReadyMaxWait)
		}
		interval := directReadyInterval
		if ready.IntervalMS > 0 {
			interval = time.Duration(ready.IntervalMS) * time.Millisecond
		}
		if time.Since(started)+interval > maxWait {
			return errDirectReadyTimeout
		}
		prevStatus = ready.Status
		if ready.JobID != "" {
			jobID = ready.JobID
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// directReadyInterval is the Mac client's poll interval when the server
// gives none. It is a variable so tests can shorten it.
var directReadyInterval = 5 * time.Second

var errDirectReadyTimeout = errors.New("client: uploaded media was not ready in time")
