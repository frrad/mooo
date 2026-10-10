package chatmeta

import (
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// ChatOnRoomCommand is the request the official client sends when a chat is
// opened. Its response carries every active member's read watermark.
const ChatOnRoomCommand = "CHATONROOM"

// ChatOnRoomRequest always carries chatId (int64), token (int64; zero without
// a stored room token) and opt (int32; zero outside open chats), matching the
// Mac request model, which sends all three even when zero.
type ChatOnRoomRequest struct {
	ChatID int64
	Token  int64
}

func (r ChatOnRoomRequest) MarshalBSON() ([]byte, error) {
	if r.ChatID <= 0 || r.Token < 0 {
		return nil, ErrInvalidRequest
	}
	return bson.Marshal(bson.D{{Key: "chatId", Value: r.ChatID}, {Key: "token", Value: r.Token}, {Key: "opt", Value: int32(0)}})
}

// ChatOnRoomResponse keeps the fields the bridge consumes. Watermarks maps an
// active member to its read watermark; w pairs positionally with a.
type ChatOnRoomResponse struct {
	ChatID     int64
	Full       bool
	Token      int64
	LastLogID  int64
	Watermarks map[int64]int64
}

func DecodeChatOnRoomResponse(body []byte) (ChatOnRoomResponse, error) {
	raw := bson.Raw(body)
	if raw.Validate() != nil {
		return ChatOnRoomResponse{}, ErrInvalidResponse
	}
	var r ChatOnRoomResponse
	var ok bool
	var err error
	if r.ChatID, ok, err = int64Field(raw, "c", "chatId"); err != nil || !ok || r.ChatID <= 0 {
		return r, fmt.Errorf("%w: chat-on chat id", ErrInvalidResponse)
	}
	if r.Full, _, err = boolField(raw, "f"); err != nil {
		return r, ErrInvalidResponse
	}
	if r.Token, _, err = int64Field(raw, "o"); err != nil || r.Token < 0 {
		return r, ErrInvalidResponse
	}
	if r.LastLogID, _, err = int64Field(raw, "l"); err != nil || r.LastLogID < 0 {
		return r, ErrInvalidResponse
	}
	members, _, err := int64Array(raw, "a")
	if err != nil {
		return r, ErrInvalidResponse
	}
	watermarks, _, err := int64Array(raw, "w")
	if err != nil || len(watermarks) != len(members) || len(members) > 10000 {
		return r, fmt.Errorf("%w: chat-on watermarks", ErrInvalidResponse)
	}
	r.Watermarks = make(map[int64]int64, len(members))
	for i, member := range members {
		if member <= 0 || watermarks[i] < 0 {
			return r, ErrInvalidResponse
		}
		if _, duplicate := r.Watermarks[member]; duplicate {
			return r, ErrInvalidResponse
		}
		r.Watermarks[member] = watermarks[i]
	}
	return r, nil
}
