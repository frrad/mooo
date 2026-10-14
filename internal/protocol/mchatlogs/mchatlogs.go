// Package mchatlogs models the independently traced regular-chat startup bulk
// acquisition path. It does not interpret source message bodies or read state.
package mchatlogs

import (
	"errors"

	"github.com/frrad/mooo/internal/protocol/bsonfield"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const MaxChats = 100

var ErrInvalid = errors.New("mchatlogs: invalid request or response")

type Request struct{ ChatIDs, Sinces []int64 }

func (r Request) MarshalBSON() ([]byte, error) {
	if len(r.ChatIDs) == 0 || len(r.ChatIDs) > MaxChats || len(r.ChatIDs) != len(r.Sinces) {
		return nil, ErrInvalid
	}
	for i, id := range r.ChatIDs {
		if id <= 0 || r.Sinces[i] < 0 || (i > 0 && id <= r.ChatIDs[i-1]) {
			return nil, ErrInvalid
		}
	}
	return bson.Marshal(bson.D{{Key: "chatIds", Value: r.ChatIDs}, {Key: "sinces", Value: r.Sinces}})
}

type Response struct {
	ChatLogs []bson.Raw
	EOF      bool
}

func ParseResponse(body []byte) (Response, error) {
	raw := bson.Raw(body)
	if raw.Validate() != nil {
		return Response{}, ErrInvalid
	}
	out := Response{}
	if value, err := raw.LookupErr("eof"); err == nil && value.Type == bson.TypeBoolean {
		out.EOF = value.Boolean()
	}
	value, err := raw.LookupErr("chatLogs")
	if err != nil || value.Type == bson.TypeNull {
		return out, nil
	}
	if value.Type != bson.TypeArray {
		return Response{}, ErrInvalid
	}
	values, err := value.Array().Values()
	if err != nil {
		return Response{}, ErrInvalid
	}
	out.ChatLogs = make([]bson.Raw, 0, len(values))
	last := map[int64]int64{}
	var previousChat int64
	for _, v := range values {
		if v.Type != bson.TypeEmbeddedDocument {
			return Response{}, ErrInvalid
		}
		log := v.Document()
		chat := bsonfield.Int64OrZero(log, "chatId")
		id, err := syncmsg.LogID(log)
		if err != nil || chat <= 0 || chat < previousChat || (last[chat] > 0 && id <= last[chat]) {
			return Response{}, ErrInvalid
		}
		last[chat] = id
		previousChat = chat
		out.ChatLogs = append(out.ChatLogs, append(bson.Raw(nil), log...))
	}
	return out, nil
}
