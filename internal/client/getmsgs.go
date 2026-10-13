package client

import (
	"context"

	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/events"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// GetMessages reads the current chat logs for logIDs in one chat with one
// GETMSGS request. Logs the server does not return are simply absent.
func (s *session) GetMessages(ctx context.Context, chatID int64, logIDs []int64, gaps gapFunc, observers ...func(int64, []bson.Raw) error) ([]events.Event, error) {
	body, err := (chat.GetMessagesRequest{ChatID: chatID, LogIDs: logIDs}).MarshalBSON()
	if err != nil {
		return nil, protocolErr(chat.GetMessagesCommand, "request")
	}
	reply, err := s.request(ctx, chat.GetMessagesCommand, body)
	if err != nil {
		return nil, err
	}
	chatLogs, err := chat.DecodeGetMessagesResponse(reply.Body)
	if err != nil {
		return nil, protocolErr(chat.GetMessagesCommand, "chatLogs")
	}
	if chatLogs == nil {
		return nil, nil
	}
	wanted := make(map[int64]bool, len(logIDs))
	for _, logID := range logIDs {
		wanted[logID] = true
	}
	result := make([]events.Event, 0, len(chatLogs))
	for _, chatLog := range chatLogs {
		event, err := events.DecodeChatLog(chatID, chatLog)
		if err != nil {
			return nil, err
		}
		gaps.observe(chat.GetMessagesCommand, event, chatLog)
		if _, logID, ok := events.MessagePosition(event); !ok || !wanted[logID] {
			return nil, protocolErr(chat.GetMessagesCommand, "chatLogs.logId")
		}
		result = append(result, event)
	}
	for _, observe := range observers {
		if err = observe(chatID, chatLogs); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// GetMessages reads chosen chat logs through the current session.
func (c *Client) GetMessages(ctx context.Context, chatID int64, logIDs []int64) ([]events.Event, error) {
	if c == nil || ctx == nil {
		return nil, ErrProtocol
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClientClosed
	}
	end := c.beginPersistenceLocked()
	store := c.historyStore
	c.mu.Unlock()
	defer end()
	return withSession(c, ctx, func(session *session) ([]events.Event, error) {
		if store != nil {
			return session.GetMessages(ctx, chatID, logIDs, c.gaps, store.observeCurrentRecords)
		}
		return session.GetMessages(ctx, chatID, logIDs, c.gaps)
	})
}
