// Package syncmsg implements the reviewed, transport-independent SYNCMSG
// history recovery contract used by the current Mac client.
package syncmsg

import (
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/bsonfield"
)

// MaxPageSize bounds Count. Count is not a page size: it declares how many
// messages in (Cur, Max] the client already holds, and the server returns the
// ones missing. The official client counts its locally stored messages in the
// range, capped at this value. A client recovering an interval it holds none
// of sends zero.
const MaxPageSize int32 = 300

var (
	ErrInvalidRequest  = errors.New("syncmsg: invalid request")
	ErrInvalidResponse = errors.New("syncmsg: invalid response")
)

var fields = bsonfield.Reader{Err: ErrInvalidResponse}

type Request struct {
	ChatID int64
	Cur    int64
	Max    int64
	Count  int32
}

// ReadRequest is the single-room sync request that acknowledges reading. It
// differs from Request only in accepting cur equal to max: the official client
// sends that empty range with cnt=0 after a response without isOK moved its
// synced position to the target.
type ReadRequest Request

func (r ReadRequest) MarshalBSON() ([]byte, error) {
	if r.Max < r.Cur {
		return nil, ErrInvalidRequest
	}
	return r.marshal()
}

func (r Request) MarshalBSON() ([]byte, error) {
	if r.Max <= r.Cur {
		return nil, ErrInvalidRequest
	}
	return ReadRequest(r).marshal()
}

// marshal validates every bound except the order of cur and max, which each
// request type checks itself.
func (r ReadRequest) marshal() ([]byte, error) {
	if r.ChatID <= 0 || r.Cur < 0 || r.Max <= 0 || r.Count < 0 || r.Count > MaxPageSize {
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
	// MinLogID is a server-supplied client-wide floor, not a per-room oldest log
	// or a retention duration. Missing/non-integer values normalize to zero.
	MinLogID int64
	// IsOK is the server's single-room sync verdict. The official client sets
	// its synced position to the request maximum only when it is true, and
	// otherwise to the highest returned log. Missing/non-boolean values are
	// false, matching the executed Mac model.
	IsOK bool
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
	chatID, ok, err := fields.Int64(raw, "c")
	if err != nil || !ok || chatID <= 0 {
		return Target{}, ErrInvalidResponse
	}
	last, err := raw.LookupErr("l")
	if err != nil || last.Type != bson.TypeEmbeddedDocument {
		return Target{}, ErrInvalidResponse
	}
	logID, ok, err := fields.Int64(last.Document(), "logId")
	if err != nil || !ok || logID <= 0 {
		return Target{}, ErrInvalidResponse
	}
	// A nested chatId of the wrong type is ignored, not rejected.
	if nestedChatID, ok, nestedErr := fields.Int64(last.Document(), "chatId"); nestedErr == nil && ok && nestedChatID != chatID {
		return Target{}, ErrInvalidResponse
	}
	// l is the last displayable chat log; ll, the last log ID, also covers
	// later feeds such as edits, which catch-up must reach.
	if lastLogID, ok, err := fields.Int64(raw, "ll"); err == nil && ok && lastLogID > logID {
		logID = lastLogID
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
	result := Response{ChatLogs: []bson.Raw{}, MinLogID: bsonfield.Int64OrZero(raw, "minLogId")}
	if value, err := raw.LookupErr("isOK"); err == nil {
		result.IsOK, _ = value.BooleanOK()
	}
	value, err := raw.LookupErr("chatLogs")
	if err != nil {
		// Live status-0 responses omit chatLogs when the page is empty.
		return result, nil
	}
	if value.Type == bson.TypeNull {
		return result, nil
	}
	if value.Type != bson.TypeArray {
		return Response{}, fmt.Errorf("%w: chatLogs type %s", ErrInvalidResponse, value.Type)
	}
	values, err := value.Array().Values()
	if err != nil {
		return Response{}, ErrInvalidResponse
	}
	result.ChatLogs = make([]bson.Raw, 0, len(values))
	var previous int64
	for _, value := range values {
		if value.Type != bson.TypeEmbeddedDocument {
			return Response{}, fmt.Errorf("%w: chatLogs element", ErrInvalidResponse)
		}
		document := value.Document()
		logID, ok, err := fields.Int64(document, "logId")
		if err != nil || !ok || logID <= 0 || (previous > 0 && logID <= previous) {
			return Response{}, fmt.Errorf("%w: chatLogs logId/order", ErrInvalidResponse)
		}
		previous = logID
		result.ChatLogs = append(result.ChatLogs, append(bson.Raw(nil), document...))
	}
	return result, nil
}

func LogID(raw bson.Raw) (int64, error) {
	value, ok, err := fields.Int64(raw, "logId")
	if err != nil || !ok || value <= 0 {
		return 0, ErrInvalidResponse
	}
	return value, nil
}
