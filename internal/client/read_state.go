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
// client's single-room sync: cur is the last position the server confirmed as
// read (zero for a chat never acknowledged), and max and cnt describe the
// oldest messages held after it, capped at 300; when held reports nothing, max
// is the watermark and cnt is zero.
//
// The server answers isOK=true, and marks the chat read through max, only when
// cnt covers every log in (cur, max]; that confirmed position is persisted, and
// a capped request continues from it. Otherwise it returns the logs and marks
// nothing read. As the official client does, MarkRead then continues from the
// highest returned log, which ends with cur equal to max and cnt=0 once it
// reaches the watermark. That intermediate position is not persisted: unlike
// the official lastSyncLogId it was never confirmed, so an interrupted
// acknowledgement repeats it rather than skipping an unread position.
//
// Catch-up SYNCMSG pages never write this checkpoint. Calls for one checkpoint
// are serialized per Client so the check/request/commit sequence is atomic; the
// profile lease ensures that only one Client owns a profile across processes.
// A failure is returned without retrying.
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
	for pending := cur < watermark; pending; {
		maxLogID, count, err := held(ctx, cur, watermark, readSyncHeldCap)
		if err != nil {
			return syncmsg.Response{}, err
		}
		if count <= 0 || maxLogID <= cur || maxLogID > watermark {
			maxLogID, count = watermark, 0
		}
		request := syncmsg.ReadRequest{ChatID: chatID, Cur: cur, Max: maxLogID, Count: int32(min(count, readSyncHeldCap))}
		response, err = withSession(c, ctx, func(session *session) (syncmsg.Response, error) {
			return call(session, ctx, "SYNCMSG", request, syncmsg.ParseResponse)
		})
		if err != nil {
			return syncmsg.Response{}, err
		}
		pending = false
		if response.IsOK {
			if checkpoint != nil {
				if _, err := checkpoint.CommitReadWatermark(chatID, maxLogID); err != nil {
					return syncmsg.Response{}, err
				}
			}
			pending = count >= readSyncHeldCap && maxLogID < watermark
			cur = maxLogID
		} else if n := len(response.ChatLogs); n > 0 {
			if last, err := syncmsg.LogID(response.ChatLogs[n-1]); err == nil && last > cur && last <= maxLogID {
				pending, cur = true, last
			}
		}
	}
	return response, nil
}
