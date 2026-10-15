package chatmeta

import (
	"encoding/json"
	"fmt"
	"strconv"

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
	// StopSyncLogMeta is the Mac chat-entry metadata-sync gate (wire dlm).
	StopSyncLogMeta bool
	// MoimRevisions maps a Boards type to its current update revision (wire
	// mr, a JSON object string). The Mac client fetches GETMOMETA for types
	// whose revision differs from its stored Boards.
	MoimRevisions map[int32]int64
}

func DecodeChatOnRoomResponse(body []byte) (ChatOnRoomResponse, error) {
	raw := bson.Raw(body)
	if raw.Validate() != nil {
		return ChatOnRoomResponse{}, ErrInvalidResponse
	}
	var r ChatOnRoomResponse
	var ok bool
	var err error
	if r.ChatID, ok, err = fields.Int64(raw, "c", "chatId"); err != nil || !ok || r.ChatID <= 0 {
		return r, fmt.Errorf("%w: chat-on chat id", ErrInvalidResponse)
	}
	if r.Full, _, err = fields.Bool(raw, "f"); err != nil {
		return r, ErrInvalidResponse
	}
	if value, lookupErr := raw.LookupErr("dlm"); lookupErr == nil {
		if value.Type != bson.TypeBoolean {
			return r, ErrInvalidResponse
		}
		r.StopSyncLogMeta = value.Boolean()
	}
	if r.MoimRevisions, err = decodeMoimRevisions(raw); err != nil {
		return r, err
	}
	if r.Token, _, err = fields.Int64(raw, "o"); err != nil || r.Token < 0 {
		return r, ErrInvalidResponse
	}
	if r.LastLogID, _, err = fields.Int64(raw, "l"); err != nil || r.LastLogID < 0 {
		return r, ErrInvalidResponse
	}
	members, _, err := fields.Int64s(raw, "a")
	if err != nil {
		return r, ErrInvalidResponse
	}
	watermarks, _, err := fields.Int64s(raw, "w")
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

func decodeMoimRevisions(raw bson.Raw) (map[int32]int64, error) {
	value, err := raw.LookupErr("mr")
	if err != nil {
		return nil, nil
	}
	text, ok := value.StringValueOK()
	if !ok || len(text) > 1<<16 {
		return nil, fmt.Errorf("%w: chat-on boards revisions", ErrInvalidResponse)
	}
	var decoded map[string]int64
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		return nil, fmt.Errorf("%w: chat-on boards revisions", ErrInvalidResponse)
	}
	out := make(map[int32]int64, len(decoded))
	for key, revision := range decoded {
		kind, err := strconv.ParseInt(key, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("%w: chat-on boards revisions", ErrInvalidResponse)
		}
		out[int32(kind)] = revision
	}
	return out, nil
}
