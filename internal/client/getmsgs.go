package client

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
)

// GetMessagesCommand reads chosen chat logs by position. The Mac client uses
// it to fetch messages edited while it was offline.
const GetMessagesCommand = "GETMSGS"

const maxGetMessages = 200

// GetMessages reads the current chat logs for logIDs in one chat with one
// GETMSGS request. Logs the server does not return are simply absent.
func (s *Session) GetMessages(ctx context.Context, chatID int64, logIDs []int64) ([]events.Event, error) {
	if s == nil || ctx == nil || chatID <= 0 || len(logIDs) == 0 || len(logIDs) > maxGetMessages {
		return nil, ErrProtocol
	}
	chatIDs := make(bson.A, 0, len(logIDs))
	logs := make(bson.A, 0, len(logIDs))
	wanted := make(map[int64]bool, len(logIDs))
	for _, logID := range logIDs {
		if logID <= 0 {
			return nil, ErrProtocol
		}
		chatIDs = append(chatIDs, chatID)
		logs = append(logs, logID)
		wanted[logID] = true
	}
	body, err := bson.Marshal(bson.D{{Key: "chatIds", Value: chatIDs}, {Key: "logIds", Value: logs}})
	if err != nil {
		return nil, ErrProtocol
	}
	reply, err := s.Request(ctx, GetMessagesCommand, body)
	if err != nil {
		return nil, err
	}
	if status, err := responseStatus(reply); err != nil || status != 0 {
		return nil, StatusError{Command: GetMessagesCommand, Status: status}
	}
	value, err := bson.Raw(reply.Body).LookupErr("chatLogs")
	if err != nil {
		return nil, nil
	}
	if value.Type != bson.TypeArray {
		return nil, ErrProtocol
	}
	values, err := value.Array().Values()
	if err != nil {
		return nil, ErrProtocol
	}
	result := make([]events.Event, 0, len(values))
	for _, item := range values {
		chatLog, ok := item.DocumentOK()
		if !ok {
			return nil, ErrProtocol
		}
		if nested, err := chatLog.LookupErr("chatId"); err == nil {
			id, ok := nested.AsInt64OK()
			if !ok || id != chatID {
				return nil, ErrProtocol
			}
		}
		wrapped, err := bson.Marshal(bson.D{{Key: "chatId", Value: chatID}, {Key: "chatLog", Value: chatLog}})
		if err != nil {
			return nil, ErrProtocol
		}
		event, err := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: wrapped})
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
	session, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	return session.GetMessages(ctx, chatID, logIDs)
}
