// Package chat contains transport-independent Kakao chat operations.
package chat

import (
	"errors"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/bsonfield"
)

const CreateCommand = "CREATE"

// fields reads response fields; callers map every failure to their own
// response error.
var fields = bsonfield.Reader{Err: ErrInvalidResponse}

var (
	ErrNoMembers       = errors.New("chat: CREATE requires at least one member")
	ErrInvalidMemberID = errors.New("chat: invalid member id")
	ErrDuplicateMember = errors.New("chat: duplicate member id")
	ErrTooManyMembers  = errors.New("chat: too many members")
	ErrInvalidResponse = errors.New("chat: invalid CREATE response")
)

const maxCreateMembers = 1000

// CreateOptions are the optional properties of the protocol's generic CREATE
// primitive. One MemberID creates a direct chat; multiple IDs create a group.
type CreateOptions struct {
	PushAlert       bool
	MemoChat        bool
	NickName        string
	ProfileImageURL string
}

// CreateRequest is the semantic request shared by direct and group chats.
type CreateRequest struct {
	MemberIDs []int64
	Options   CreateOptions
}

func (r CreateRequest) Validate() error {
	if len(r.MemberIDs) == 0 {
		return ErrNoMembers
	}
	if len(r.MemberIDs) > maxCreateMembers {
		return ErrTooManyMembers
	}
	seen := make(map[int64]struct{}, len(r.MemberIDs))
	for _, id := range r.MemberIDs {
		if id <= 0 {
			return ErrInvalidMemberID
		}
		if _, ok := seen[id]; ok {
			return ErrDuplicateMember
		}
		seen[id] = struct{}{}
	}
	if strings.IndexByte(r.Options.NickName, 0) >= 0 || strings.IndexByte(r.Options.ProfileImageURL, 0) >= 0 {
		return errors.New("chat: CREATE strings contain NUL")
	}
	return nil
}

// MarshalBSON preserves the current Mac client's observed field names and
// widths. Nil object properties are omitted by its serializer.
func (r CreateRequest) MarshalBSON() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	doc := bson.D{
		{Key: "memberIds", Value: append([]int64(nil), r.MemberIDs...)},
		{Key: "pushAlert", Value: r.Options.PushAlert},
		{Key: "memoChat", Value: r.Options.MemoChat},
	}
	if r.Options.NickName != "" {
		doc = append(doc, bson.E{Key: "nickName", Value: r.Options.NickName})
	}
	if r.Options.ProfileImageURL != "" {
		doc = append(doc, bson.E{Key: "profileImageUrl", Value: r.Options.ProfileImageURL})
	}
	out, err := bson.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("chat: encode CREATE: %w", err)
	}
	return out, nil
}

// CreateResponse keeps the complete chatRoom document for forward-compatible
// room-state decoding while exposing the stable server chat ID.
type CreateResponse struct {
	ChatID   int64
	ChatRoom bson.Raw
}

func DecodeCreateResponse(body []byte) (CreateResponse, error) {
	raw := bson.Raw(body)
	chatID, ok, err := fields.Int64(raw, "chatId")
	if err != nil || !ok || chatID <= 0 {
		return CreateResponse{}, ErrInvalidResponse
	}
	room, ok, err := fields.Document(raw, "chatRoom")
	if err != nil || !ok {
		return CreateResponse{}, ErrInvalidResponse
	}
	return CreateResponse{ChatID: chatID, ChatRoom: room}, nil
}
