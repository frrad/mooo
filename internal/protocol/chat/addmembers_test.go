package chat

import (
	"go.mongodb.org/mongo-driver/v2/bson"
	"testing"
)

func TestAddMembersWireAndValidation(t *testing.T) {
	for _, r := range []AddMembersRequest{{}, {ChatID: 1}, {ChatID: 1, MemberIDs: []int64{0}}, {ChatID: 1, MemberIDs: []int64{2, 2}}} {
		if _, err := r.MarshalBSON(); err == nil {
			t.Fatalf("invalid request accepted: %+v", r)
		}
	}
	body, err := (AddMembersRequest{ChatID: 5000, MemberIDs: []int64{4000}}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(body)
	if raw.Lookup("chatId").Type != bson.TypeInt64 || raw.Lookup("chatId").Int64() != 5000 {
		t.Fatal("chat ID wire type/value")
	}
	values, err := raw.Lookup("memberIds").Array().Values()
	if err != nil || len(values) != 1 || values[0].Type != bson.TypeInt64 || values[0].Int64() != 4000 {
		t.Fatal("selected member wire type/value")
	}
}

func TestDecodeAddMembersOptionalLogAndWarning(t *testing.T) {
	for _, doc := range []bson.D{{}, {{Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(7)}}}, {Key: "warningMsg", Value: "synthetic warning"}}} {
		body, err := bson.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		out, err := DecodeAddMembersResponse(body)
		if err != nil {
			t.Fatal(err)
		}
		if len(doc) > 0 && (out.Warning != "synthetic warning" || out.ChatLog.Lookup("logId").Int64() != 7) {
			t.Fatal("response fields lost")
		}
	}
	for _, doc := range []bson.D{{{Key: "chatLog", Value: "wrong"}}, {{Key: "warningMsg", Value: int64(1)}}} {
		body, err := bson.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = DecodeAddMembersResponse(body); err == nil {
			t.Fatal("malformed response accepted")
		}
	}
}
