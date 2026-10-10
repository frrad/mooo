package client

import (
	"context"

	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/events"
)

// GetMessages reads the current chat logs for logIDs in one chat with one
// GETMSGS request. Logs the server does not return are simply absent.
func (s *Session) GetMessages(ctx context.Context, chatID int64, logIDs []int64) ([]events.Event, error) {
	body, err := (chat.GetMessagesRequest{ChatID: chatID, LogIDs: logIDs}).MarshalBSON()
	if err != nil {
		return nil, ErrProtocol
	}
	reply, err := s.Request(ctx, chat.GetMessagesCommand, body)
	if err != nil {
		return nil, err
	}
	chatLogs, err := chat.DecodeGetMessagesResponse(reply.Body)
	if err != nil {
		return nil, ErrProtocol
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
		if _, logID, ok := events.MessagePosition(event); !ok || !wanted[logID] {
			return nil, ErrProtocol
		}
		result = append(result, event)
	}
	return result, nil
}

// GetMessages reads chosen chat logs through the current session.
func (c *Client) GetMessages(ctx context.Context, chatID int64, logIDs []int64) ([]events.Event, error) {
	return withSession(c, ctx, func(session *Session) ([]events.Event, error) {
		return session.GetMessages(ctx, chatID, logIDs)
	})
}
