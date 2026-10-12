package connector

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestJoinedAsAcceptsDoublePuppetForSelf(t *testing.T) {
	kc := &KakaoClient{userID: 1000, login: &bridgev2.UserLogin{UserLogin: &database.UserLogin{UserMXID: "@me:example"}}}
	joined := &event.MemberEventContent{Membership: event.MembershipJoin}
	members := map[id.UserID]*event.MemberEventContent{"@me:example": joined}
	if !kc.joinedAs(members, makeUserID(1000), "@kakao_1000:example") {
		t.Fatal("own real account should count as the self member")
	}
	if kc.joinedAs(members, makeUserID(2000), "@kakao_2000:example") {
		t.Fatal("another member must still need its own ghost joined")
	}
	members["@kakao_2000:example"] = joined
	if !kc.joinedAs(members, makeUserID(2000), "@kakao_2000:example") {
		t.Fatal("joined ghost should count")
	}
}
