// Package chatlog decodes the envelope shared by MSG-shaped LOCO packets: a
// top-level chatId and an embedded chatLog document carrying the message's
// logId, type, authorId and sendAt. Packages that decode message content take
// the decoded Envelope instead of re-parsing the packet body.
package chatlog

import (
	"errors"
	"math"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/bsonfield"
)

var ErrMalformed = errors.New("chatlog: malformed envelope")

var fields = bsonfield.Reader{Err: ErrMalformed}

// Envelope is a validated MSG identity plus the chatLog document whose
// content-specific fields the caller decodes.
type Envelope struct {
	ChatID int64
	LogID  int64
	// AuthorID and SentAt are zero when absent or not an integer.
	AuthorID int64
	SentAt   int64
	Type     int32
	// Log is the embedded chatLog document. It aliases the packet body.
	Log bson.Raw
	// LogIDInChatLog is false when chatLog.logId is missing or not an
	// integer and LogID came from the packet's top-level logId instead.
	LogIDInChatLog bool
}

// Decode validates body and reads its envelope. On top of DecodeIdentity it
// requires chatLog.type to be an integer in [0, MaxInt32].
func Decode(body []byte) (Envelope, error) {
	envelope, err := DecodeIdentity(body)
	if err != nil {
		return Envelope{}, err
	}
	messageType, ok, err := fields.Int64(envelope.Log, "type")
	if err != nil || !ok || messageType < 0 || messageType > math.MaxInt32 {
		return Envelope{}, ErrMalformed
	}
	envelope.Type = int32(messageType)
	return envelope, nil
}

// DecodeIdentity validates body and reads the message identity without
// interpreting chatLog.type; Type is left zero. chatId must be a positive
// integer, chatLog an embedded document, and logId a positive integer (from
// chatLog, else from the top level). Integers may be BSON int32 or int64.
func DecodeIdentity(body []byte) (Envelope, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil {
		return Envelope{}, ErrMalformed
	}
	chatID, ok, err := fields.Int64(raw, "chatId")
	if err != nil || !ok || chatID <= 0 {
		return Envelope{}, ErrMalformed
	}
	logValue, err := raw.LookupErr("chatLog")
	if err != nil || logValue.Type != bson.TypeEmbeddedDocument {
		return Envelope{}, ErrMalformed
	}
	log := logValue.Document()
	logID, inner, err := fields.Int64(log, "logId")
	if err != nil || !inner {
		inner = false
		logID, ok, err = fields.Int64(raw, "logId")
		if err != nil || !ok {
			return Envelope{}, ErrMalformed
		}
	}
	if logID <= 0 {
		return Envelope{}, ErrMalformed
	}
	return Envelope{
		ChatID:         chatID,
		LogID:          logID,
		AuthorID:       bsonfield.Int64OrZero(log, "authorId"),
		SentAt:         bsonfield.Int64OrZero(log, "sendAt"),
		Log:            log,
		LogIDInChatLog: inner,
	}, nil
}
