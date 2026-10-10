package chatmeta

import (
	"encoding/json"
	"go.mongodb.org/mongo-driver/v2/bson"
	"os"
	"reflect"
	"testing"
)

func TestExecutedMemberRequestFixture(t *testing.T) {
	raw, err := os.ReadFile("../../../research/fixtures/group-profiles/member-runtime.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Request struct {
			Method string
			JSON   string
			Input  struct {
				ChatID    int64
				MemberIDs []int64
			}
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	var expected struct {
		ChatID    int64
		MemberIDs []int64
	}
	if err = json.Unmarshal([]byte(fixture.Request.JSON), &expected); err != nil {
		t.Fatal(err)
	}
	body, err := (MemberRequest{ChatID: fixture.Request.Input.ChatID, MemberIDs: fixture.Request.Input.MemberIDs}).MarshalBSON()
	if err != nil {
		t.Fatal(err)
	}
	doc := bson.Raw(body)
	if fixture.Request.Method != MemberCommand || doc.Lookup("chatId").Type != bson.TypeInt64 || doc.Lookup("chatId").Int64() != expected.ChatID {
		t.Fatal("production request identity differs from executed wire construction")
	}
	values, err := doc.Lookup("memberIds").Array().Values()
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, v := range values {
		if v.Type != bson.TypeInt64 {
			t.Fatal("member identity is not int64")
		}
		ids = append(ids, v.Int64())
	}
	if !reflect.DeepEqual(ids, expected.MemberIDs) {
		t.Fatal("production selected identities differ from executed request")
	}
}
