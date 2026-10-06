package client

import (
	"context"

	"github.com/frrad/mooo/internal/protocol/syncmsg"
)

// MarkRead acknowledges one exact message position through the same
// SYNCMSG primitive used for bounded history recovery. The server-side
// acknowledgement is the one-message interval [watermark-1, watermark].
// Request sends exactly once; transport failures and non-zero statuses are
// returned by the shared request path without retrying.
func (s *Session) MarkRead(ctx context.Context, chatID, watermark int64) (syncmsg.Response, error) {
	return s.SyncMessages(ctx, syncmsg.Request{
		ChatID: chatID,
		Cur:    watermark - 1,
		Max:    watermark,
		Count:  1,
	})
}

// MarkRead persists a successful read acknowledgement so a restarted client
// does not send the same watermark again. Only MarkRead writes this
// checkpoint; catch-up SYNCMSG pages never suppress an acknowledgement. Calls for one checkpoint are
// serialized per Client so the check/request/commit sequence is atomic. Open's
// profile lease ensures that only one Client owns a profile across processes.
func (c *Client) MarkRead(ctx context.Context, chatID, watermark int64) (syncmsg.Response, error) {
	if c == nil || ctx == nil {
		return syncmsg.Response{}, ErrProtocol
	}
	request := syncmsg.Request{ChatID: chatID, Cur: watermark - 1, Max: watermark, Count: 1}
	if _, err := request.MarshalBSON(); err != nil {
		return syncmsg.Response{}, err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return syncmsg.Response{}, ErrClientClosed
	}
	checkpoint := c.checkpoint
	endPersistence := c.beginPersistenceLocked()
	c.mu.Unlock()
	defer endPersistence()

	c.readMu.Lock()
	defer c.readMu.Unlock()
	if checkpoint != nil && checkpoint.ReadWatermark(chatID) >= watermark {
		return syncmsg.Response{}, nil
	}

	session, err := c.ensureSession(ctx)
	if err != nil {
		return syncmsg.Response{}, err
	}
	response, err := session.MarkRead(ctx, chatID, watermark)
	if err != nil {
		return syncmsg.Response{}, err
	}
	if checkpoint != nil {
		if _, err := checkpoint.CommitReadWatermark(chatID, watermark); err != nil {
			return syncmsg.Response{}, err
		}
	}
	return response, nil
}
