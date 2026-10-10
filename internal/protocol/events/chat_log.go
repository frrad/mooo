package events

import (
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/bsonfield"
	"github.com/frrad/mooo/internal/protocol/loco"
)

// ErrChatLogMisdirected means a chat log read for one chat names another.
var ErrChatLogMisdirected = fmt.Errorf("%w: chat log names another chat", ErrMalformedEvent)

// DecodeChatLog decodes a bare chat log returned for chatID (a SYNCMSG or
// GETMSGS chatLogs entry) the way DecodeForDelivery decodes an MSG push
// carrying it. A nested chatLog.chatId, when present and not null, must be a
// BSON int32 or int64 equal to chatID, so a reply cannot redirect content into
// another chat; otherwise the error is ErrChatLogMisdirected.
func DecodeChatLog(chatID int64, chatLog bson.Raw) (Event, error) {
	if value, err := chatLog.LookupErr("chatId"); err == nil && value.Type != bson.TypeNull {
		if nested, ok := bsonfield.Int64Value(value); !ok || nested != chatID {
			return nil, ErrChatLogMisdirected
		}
	}
	packet, err := chatLogPacket(chatID, chatLog)
	if err != nil {
		return nil, err
	}
	return DecodeForDelivery(packet)
}

// chatLogPacket wraps a chat log in the MSG packet that would carry it.
func chatLogPacket(chatID int64, chatLog bson.Raw) (loco.Packet, error) {
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: chatID}, {Key: "chatLog", Value: chatLog}})
	if err != nil {
		return loco.Packet{}, ErrMalformedEvent
	}
	return loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body}, nil
}
