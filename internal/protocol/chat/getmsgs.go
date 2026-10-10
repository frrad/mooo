package chat

import (
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// GetMessagesCommand reads chosen chat logs by position. The Mac client uses
// it to fetch messages edited while it was offline.
const GetMessagesCommand = "GETMSGS"

// MaxGetMessages bounds the log IDs one GETMSGS request names.
const MaxGetMessages = 200

var ErrInvalidGetMessagesResp = errors.New("chat: invalid GETMSGS response")

// GetMessagesRequest names chat logs in one chat. The wire request carries
// parallel chatIds and logIds arrays, so ChatID repeats once per log ID.
type GetMessagesRequest struct {
	ChatID int64
	LogIDs []int64
}

func (r GetMessagesRequest) MarshalBSON() ([]byte, error) {
	if r.ChatID <= 0 {
		return nil, ErrInvalidChatID
	}
	if len(r.LogIDs) == 0 || len(r.LogIDs) > MaxGetMessages {
		return nil, ErrInvalidMessageID
	}
	chatIDs := make(bson.A, 0, len(r.LogIDs))
	logIDs := make(bson.A, 0, len(r.LogIDs))
	for _, logID := range r.LogIDs {
		if logID <= 0 {
			return nil, ErrInvalidMessageID
		}
		chatIDs = append(chatIDs, r.ChatID)
		logIDs = append(logIDs, logID)
	}
	return bson.Marshal(bson.D{{Key: "chatIds", Value: chatIDs}, {Key: "logIds", Value: logIDs}})
}

// DecodeGetMessagesResponse returns the reply's chatLogs documents in wire
// order. A reply without chatLogs returned no logs; logs the server does not
// return are simply absent. The documents alias body.
func DecodeGetMessagesResponse(body []byte) ([]bson.Raw, error) {
	value, err := bson.Raw(body).LookupErr("chatLogs")
	if err != nil {
		return nil, nil
	}
	if value.Type != bson.TypeArray {
		return nil, ErrInvalidGetMessagesResp
	}
	values, err := value.Array().Values()
	if err != nil {
		return nil, ErrInvalidGetMessagesResp
	}
	logs := make([]bson.Raw, 0, len(values))
	for _, item := range values {
		chatLog, ok := item.DocumentOK()
		if !ok {
			return nil, ErrInvalidGetMessagesResp
		}
		logs = append(logs, chatLog)
	}
	return logs, nil
}
