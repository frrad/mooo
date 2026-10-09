package chat

import (
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const AddMembersCommand = "ADDMEM"

// AddMembersRequest selects participants for one explicit invitation to an
// existing chat. It does not create a room or imply that every ID was accepted.
type AddMembersRequest struct {
	ChatID    int64
	MemberIDs []int64
}

func (r AddMembersRequest) MarshalBSON() ([]byte, error) {
	if r.ChatID <= 0 {
		return nil, ErrInvalidChatID
	}
	if err := (CreateRequest{MemberIDs: r.MemberIDs}).Validate(); err != nil {
		return nil, err
	}
	return bson.Marshal(bson.D{{Key: "chatId", Value: r.ChatID}, {Key: "memberIds", Value: append([]int64(nil), r.MemberIDs...)}})
}

// AddMembersResponse preserves the invitation feed and warning. Success still
// requires the caller to obtain an authoritative roster.
type AddMembersResponse struct {
	ChatLog bson.Raw
	Warning string
}

func DecodeAddMembersResponse(body []byte) (AddMembersResponse, error) {
	raw := bson.Raw(body)
	if err := raw.Validate(); err != nil {
		return AddMembersResponse{}, err
	}
	var out AddMembersResponse
	if value, err := raw.LookupErr("chatLog"); err == nil && value.Type != bson.TypeNull {
		if value.Type != bson.TypeEmbeddedDocument {
			return out, errors.New("chat: invalid invitation log")
		}
		out.ChatLog = append(bson.Raw(nil), value.Document()...)
	}
	if value, err := raw.LookupErr("warningMsg"); err == nil && value.Type != bson.TypeNull {
		if value.Type != bson.TypeString {
			return out, errors.New("chat: invalid invitation warning")
		}
		out.Warning = value.StringValue()
	}
	return out, nil
}
