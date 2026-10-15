package chatmeta

import (
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// ChatRoom is the room model a CREATE reply carries as chatRoom (Mac
// LocoChatRoom, 26.8.0). Its name mapping uses short keys (c, t, a, m); the
// observed CREATE reply uses the property names (chatId, type,
// activeMemberIds, members, metas), so both are accepted. In full-data mode the
// official client upserts the room, including its members, from it.
type ChatRoom struct {
	ChatID          int64
	Type            string
	ActiveMemberIDs []int64
	Members         []Member
	Metas           []ChatMeta
}

func DecodeChatRoom(raw bson.Raw) (ChatRoom, error) {
	if raw.Validate() != nil {
		return ChatRoom{}, ErrInvalidResponse
	}
	var room ChatRoom
	var ok bool
	var err error
	if room.ChatID, ok, err = fields.Int64(raw, "c", "chatId"); err != nil || !ok || room.ChatID <= 0 {
		return ChatRoom{}, fmt.Errorf("%w: chat room id", ErrInvalidResponse)
	}
	if room.Type, _, err = fields.String(raw, "t", "type"); err != nil {
		return ChatRoom{}, err
	}
	if room.ActiveMemberIDs, _, err = fields.Int64s(raw, "a", "activeMemberIds"); err != nil {
		return ChatRoom{}, fmt.Errorf("%w: active members", ErrInvalidResponse)
	}
	seen := make(map[int64]bool, len(room.ActiveMemberIDs))
	for _, userID := range room.ActiveMemberIDs {
		if userID <= 0 || seen[userID] {
			return ChatRoom{}, fmt.Errorf("%w: active members", ErrInvalidResponse)
		}
		seen[userID] = true
	}
	if value, ok := fields.Lookup(raw, "m", "members"); ok && value.Type != bson.TypeNull {
		if room.Members, err = decodeMembers(value); err != nil {
			return ChatRoom{}, err
		}
	}
	if room.Metas, err = decodeChatMetas(raw, "metas"); err != nil {
		return ChatRoom{}, err
	}
	return room, nil
}
