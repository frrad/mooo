package chat

import (
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestCreateRequestMarshalDirectChat(t *testing.T) {
	body, err := (CreateRequest{MemberIDs: []int64{42}}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(body)
	if got := raw.Lookup("memberIds"); got.Type != bson.TypeArray {
		t.Fatalf("memberIds type = %v", got.Type)
	}
	values, err := raw.Lookup("memberIds").Array().Values()
	if err != nil || len(values) != 1 || values[0].Type != bson.TypeInt64 || values[0].Int64() != 42 {
		t.Fatalf("memberIds = %#v, err=%v", values, err)
	}
	if raw.Lookup("pushAlert").Type != bson.TypeBoolean || raw.Lookup("pushAlert").Boolean() {
		t.Fatal("pushAlert must be false BSON boolean")
	}
	if raw.Lookup("memoChat").Type != bson.TypeBoolean || raw.Lookup("memoChat").Boolean() {
		t.Fatal("memoChat must be false BSON boolean")
	}
	if _, err := raw.LookupErr("nickName"); err == nil {
		t.Fatal("empty nickName must be omitted")
	}
	if _, err := raw.LookupErr("profileImageUrl"); err == nil {
		t.Fatal("empty profileImageUrl must be omitted")
	}
}

func TestCreateRequestValidation(t *testing.T) {
	for name, tc := range map[string]struct {
		request CreateRequest
		want    error
	}{
		"empty":     {CreateRequest{}, ErrNoMembers},
		"zero":      {CreateRequest{MemberIDs: []int64{0}}, ErrInvalidMemberID},
		"duplicate": {CreateRequest{MemberIDs: []int64{4, 4}}, ErrDuplicateMember},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tc.request.MarshalBSON()
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDecodeCreateResponse(t *testing.T) {
	room, err := bson.Marshal(bson.D{{Key: "id", Value: int64(91)}, {Key: "type", Value: "DirectChat"}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: int64(91)}, {Key: "chatRoom", Value: bson.Raw(room)}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := DecodeCreateResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if response.ChatID != 91 || response.ChatRoom.Lookup("type").StringValue() != "DirectChat" {
		t.Fatalf("unexpected response: id=%d", response.ChatID)
	}
}
