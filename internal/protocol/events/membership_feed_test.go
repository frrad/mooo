package events

import (
	"encoding/json"
	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
	"os"
	"testing"
)

func TestMembershipMessageFeedExecutedMacFixture(t *testing.T) {
	data, err := os.ReadFile("../../../research/fixtures/membership/message-feed.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Method, Message string
			Members         []struct {
				UserID   int64 `json:"user_id"`
				UserType int32 `json:"user_type"`
			}
		}
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, c := range fixture.Cases {
		t.Run(c.Method, func(t *testing.T) {
			body, e := bson.Marshal(bson.D{{Key: "chatLog", Value: bson.D{{Key: "chatId", Value: int64(42)}, {Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(0)}, {Key: "message", Value: c.Message}}}})
			if e != nil {
				t.Fatal(e)
			}
			decoded, e := Decode(loco.Packet{Header: loco.Header{Method: c.Method}, Body: body})
			if e != nil {
				t.Fatal(e)
			}
			var members []MemberIdentity
			switch v := decoded.(type) {
			case MemberRemoved:
				if v.ChatID != 42 || v.LogID != 101 {
					t.Fatal("position")
				}
				members = []MemberIdentity{{UserID: v.UserID, UserType: v.UserType}}
			case MemberAdded:
				if v.ChatID != 42 || v.LogID != 101 {
					t.Fatal("position")
				}
				members = v.Members
			default:
				t.Fatalf("event %T", decoded)
			}
			if len(members) != len(c.Members) {
				t.Fatal("member count")
			}
			for i, expected := range c.Members {
				if members[i].UserID != expected.UserID || members[i].UserType != expected.UserType {
					t.Fatalf("member %d: %#v", i, members[i])
				}
			}
		})
	}
}

func TestMembershipFeedRejectsAmbiguousIdentityAndPreservesInt64(t *testing.T) {
	cases := []struct {
		message string
		want    int64
	}{
		{`{"feedType":2,"member":{"userId":9007199254740993}}`, 9007199254740993},
		{`{"feedType":2,"member":{"userId":7,"userId":8}}`, 0},
		{`{"feedType":2,"member":{"userId":7.5}}`, 0},
		{`{"feedType":2,"member":{"userId":-7}}`, 0},
		{`{"feedType":2,"member":{}}`, 0},
		{`{"feedType":1,"member":{"userId":7}}`, 0},
		{`{"feedType":2,"member":{"userId":7,"userType":2147483648}}`, 0},
	}
	for _, c := range cases {
		body, err := bson.Marshal(bson.D{{Key: "chatLog", Value: bson.D{{Key: "chatId", Value: int64(42)}, {Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(0)}, {Key: "message", Value: c.message}}}})
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := Decode(loco.Packet{Header: loco.Header{Method: "DELMEM"}, Body: body})
		if c.want == 0 {
			if err == nil {
				t.Fatal("ambiguous membership accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if decoded.(MemberRemoved).UserID != c.want {
			t.Fatal("identity lost precision")
		}
	}
}
