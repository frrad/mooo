package client

import (
	"context"
	"sync"

	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
)

// SessionMarkRead acknowledges one exact message position through the same
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
// does not send the same watermark again. Calls for one checkpoint are
// serialized across Client values to keep the check/request/commit sequence
// atomic without adding synchronization state to Client itself.
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
	c.mu.Unlock()

	lock := readWatermarkLock(checkpoint)
	lock.Lock()
	defer lock.Unlock()
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

var (
	readWatermarkLocks sync.Map
	readWatermarkNoop  sync.Mutex
)

func readWatermarkLock(checkpoint *continuity.Store) *sync.Mutex {
	if checkpoint == nil {
		return &readWatermarkNoop
	}
	lock, _ := readWatermarkLocks.LoadOrStore(checkpoint, &sync.Mutex{})
	return lock.(*sync.Mutex)
}
