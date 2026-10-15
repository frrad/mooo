package client

import (
	"context"

	"github.com/frrad/mooo/internal/protocol/syncmsg"
)

// readSyncHeldCap is the official client's cap on the held messages one
// mark-read SYNCMSG declares. A capped request is followed by another one
// starting at its maximum.
const readSyncHeldCap = 300

// HeldMessages reports the messages the caller already stores in the open-closed
// interval (after, upTo]: among the first limit of them in ascending log order,
// the highest log ID and their count. A zero count means none are stored.
type HeldMessages func(ctx context.Context, after, upTo int64, limit int) (maxLogID int64, count int, err error)

// MarkRead acknowledges a chat as read through watermark with the official
// client's single-room sync shape: cur is the last acknowledged position (the
// analogue of its lastSyncLogId), and max and cnt describe the messages held
// after it, capped at 300. When held reports nothing, max is the watermark and
// cnt is zero. Each successful response persists its max, and a capped request
// continues from there until the watermark is reached, so a restarted client
// does not send an acknowledged position again. Catch-up SYNCMSG pages never
// write this checkpoint. Calls for one checkpoint are serialized per Client so
// the check/request/commit sequence is atomic; the profile lease ensures that
// only one Client owns a profile across processes. A failure is returned
// without retrying.
func (c *Client) MarkRead(ctx context.Context, chatID, watermark int64, held HeldMessages) (syncmsg.Response, error) {
	if c == nil || ctx == nil || held == nil {
		return syncmsg.Response{}, ErrProtocol
	}
	if _, err := (syncmsg.Request{ChatID: chatID, Cur: watermark - 1, Max: watermark}).MarshalBSON(); err != nil {
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
	var cur int64
	if checkpoint != nil {
		cur = checkpoint.ReadWatermark(chatID)
	}
	var response syncmsg.Response
	for cur < watermark {
		maxLogID, count, err := held(ctx, cur, watermark, readSyncHeldCap)
		if err != nil {
			return syncmsg.Response{}, err
		}
		if count <= 0 || maxLogID <= cur || maxLogID > watermark {
			maxLogID, count = watermark, 0
		}
		request := syncmsg.Request{ChatID: chatID, Cur: cur, Max: maxLogID, Count: int32(min(count, readSyncHeldCap))}
		response, err = withSession(c, ctx, func(session *session) (syncmsg.Response, error) {
			return session.syncMessages(ctx, request)
		})
		if err != nil {
			return syncmsg.Response{}, err
		}
		if checkpoint != nil {
			if _, err := checkpoint.CommitReadWatermark(chatID, maxLogID); err != nil {
				return syncmsg.Response{}, err
			}
		}
		if count < readSyncHeldCap {
			break
		}
		cur = maxLogID
	}
	return response, nil
}
