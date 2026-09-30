// Package syncmsg implements the reviewed, transport-independent SYNCMSG
// history recovery contract used by the current Mac client.
package syncmsg

import (
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const MaxPageSize int32 = 300

var (
	ErrInvalidRequest  = errors.New("syncmsg: invalid request")
	ErrInvalidResponse = errors.New("syncmsg: invalid response")
)

type Request struct {
	ChatID int64
	Cur    int64
	Max    int64
	Count  int32
}

func (r Request) MarshalBSON() ([]byte, error) {
	if r.ChatID <= 0 || r.Cur < 0 || r.Max <= r.Cur || r.Count <= 0 || r.Count > MaxPageSize {
		return nil, ErrInvalidRequest
	}
	return bson.Marshal(bson.D{
		{Key: "chatId", Value: r.ChatID},
		{Key: "cur", Value: r.Cur},
		{Key: "max", Value: r.Max},
		{Key: "cnt", Value: r.Count},
	})
}

type Response struct {
	ChatLogs []bson.Raw
}

type Target struct {
	ChatID   int64
	MaxLogID int64
}

// TargetFromChatData extracts the current recovery ceiling from one reviewed
// LOGINLIST/LCHATLIST chat-data document: c is the chat ID and l is its current
// last chat log.
func TargetFromChatData(raw bson.Raw) (Target, error) {
	if err := raw.Validate(); err != nil {
		return Target{}, ErrInvalidResponse
	}
	chatID, err := integer(raw, "c")
	if err != nil || chatID <= 0 {
		return Target{}, ErrInvalidResponse
	}
	last, err := raw.LookupErr("l")
	if err != nil || last.Type != bson.TypeEmbeddedDocument {
		return Target{}, ErrInvalidResponse
	}
	logID, err := integer(last.Document(), "logId")
	if err != nil || logID <= 0 {
		return Target{}, ErrInvalidResponse
	}
	if nestedChatID, nestedErr := integer(last.Document(), "chatId"); nestedErr == nil && nestedChatID != chatID {
		return Target{}, ErrInvalidResponse
	}
	return Target{ChatID: chatID, MaxLogID: logID}, nil
}

// ParseResponse validates the chatLogs array without interpreting message
// bodies. Status is handled by the shared carriage request path.
func ParseResponse(body []byte) (Response, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil {
		return Response{}, ErrInvalidResponse
	}
	value, err := raw.LookupErr("chatLogs")
	if err != nil || value.Type != bson.TypeArray {
		return Response{}, ErrInvalidResponse
	}
	values, err := value.Array().Values()
	if err != nil {
		return Response{}, ErrInvalidResponse
	}
	result := Response{ChatLogs: make([]bson.Raw, 0, len(values))}
	var previous int64
	for _, value := range values {
		if value.Type != bson.TypeEmbeddedDocument {
			return Response{}, ErrInvalidResponse
		}
		document := value.Document()
		logID, err := integer(document, "logId")
		if err != nil || logID <= 0 || (previous > 0 && logID <= previous) {
			return Response{}, ErrInvalidResponse
		}
		previous = logID
		result.ChatLogs = append(result.ChatLogs, append(bson.Raw(nil), document...))
	}
	return result, nil
}

func LogID(raw bson.Raw) (int64, error) {
	value, err := integer(raw, "logId")
	if err != nil || value <= 0 {
		return 0, ErrInvalidResponse
	}
	return value, nil
}

func integer(raw bson.Raw, key string) (int64, error) {
	value, err := raw.LookupErr(key)
	if err != nil {
		return 0, err
	}
	switch value.Type {
	case bson.TypeInt32:
		return int64(value.Int32()), nil
	case bson.TypeInt64:
		return value.Int64(), nil
	default:
		return 0, ErrInvalidResponse
	}
}
