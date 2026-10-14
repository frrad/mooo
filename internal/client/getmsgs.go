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
	if s == nil || ctx == nil {
		return nil, ErrProtocol
	}
	body, err := (chat.GetMessagesRequest{ChatID: chatID, LogIDs: logIDs}).MarshalBSON()
	if err != nil {
		return nil, protocolErr(chat.GetMessagesCommand, "request")
	}
	fetch := func(ctx context.Context) ([]byte, error) {
		reply, err := s.request(ctx, chat.GetMessagesCommand, body)
		if err != nil {
			return nil, err
		}
		_, logs, err := decodeExactHistory(chatID, logIDs, reply.Body, nil)
		if err != nil {
			return nil, err
		}
		if logs != nil {
			for _, observe := range observers {
				if err = observe(chatID, logs); err != nil {
					return nil, err
				}
			}
		}
		return append([]byte(nil), reply.Body...), nil
	}
	var payload []byte
	if s != nil && s.historyAdmission != nil {
		payload, err = s.historyAdmission.exchange(ctx, chat.GetMessagesCommand+"/"+historyDigest(string(body)), fetch)
	} else {
		payload, err = fetch(ctx)
	}
	if err != nil {
		return nil, err
	}
	result, _, err := decodeExactHistory(chatID, logIDs, payload, gaps)
	return result, err
}

func decodeExactHistory(chatID int64, logIDs []int64, body []byte, gaps gapFunc) ([]events.Event, []bson.Raw, error) {
	logs, err := chat.DecodeGetMessagesResponse(body)
	if err != nil {
		return nil, nil, protocolErr(chat.GetMessagesCommand, "chatLogs")
	}
	if logs == nil {
		return nil, nil, nil
	}
	wanted := make(map[int64]bool, len(logIDs))
	for _, id := range logIDs {
		wanted[id] = true
	}
	result := make([]events.Event, 0, len(logs))
	for _, raw := range logs {
		evt, err := events.DecodeChatLog(chatID, raw)
		if err != nil {
			return nil, nil, err
		}
		if _, id, ok := events.MessagePosition(evt); !ok || !wanted[id] {
			return nil, nil, protocolErr(chat.GetMessagesCommand, "chatLogs.logId")
		}
		gaps.observe(chat.GetMessagesCommand, evt, raw)
		result = append(result, evt)
	}
	return result, logs, nil
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
