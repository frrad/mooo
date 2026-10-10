package connector

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestProjectChatInfoDirectChatWithCompleteRoster(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, makeUserLoginID(testSelfID))}}
	snap := chatSnapshot{
		data: chatmeta.ChatData{
			ChatID: testChatID,
			Type:   "DirectChat",
			Meta:   &chatmeta.RoomMeta{Name: "Synthetic room", ImageURL: "https://talk.kakaocdn.net/synthetic-room"},
		},
		roster:   []int64{testSelfID, testOtherID},
		profiles: []chatmeta.Member{{UserID: testOtherID, Nickname: "Synthetic user"}},
	}

	info, err := kc.projectChatInfo(portal, snap)
	if err != nil {
		t.Fatal(err)
	}

	if info.Name == nil || *info.Name != "Synthetic room" {
		t.Fatalf("name = %v, want Synthetic room", info.Name)
	}
	if info.Avatar == nil || info.Avatar.Remove {
		t.Fatalf("avatar = %+v, want the room image", info.Avatar)
	}
	if info.Type == nil || *info.Type != database.RoomTypeDM {
		t.Fatalf("room type = %v, want DM", info.Type)
	}
	if info.Members == nil || !info.Members.IsFull || info.Members.TotalMemberCount != 2 || len(info.Members.MemberMap) != 2 {
		t.Fatalf("members = %+v, want two full members", info.Members)
	}
	if info.Members.OtherUserID != makeUserID(testOtherID) {
		t.Fatalf("other user = %q, want %q", info.Members.OtherUserID, makeUserID(testOtherID))
	}
	other, ok := info.Members.MemberMap[makeUserID(testOtherID)]
	if !ok || other.Membership != event.MembershipJoin || other.UserInfo == nil || *other.UserInfo.Name != "Synthetic user" {
		t.Fatalf("other member = %+v, want joined with the MEMBER nickname", other)
	}
	self, selfOK := info.Members.MemberMap[makeUserID(testSelfID)]
	if !selfOK || !self.IsFromMe || self.UserInfo != nil {
		t.Fatalf("self member = %+v, want the local account without profile fields", self)
	}
	if info.UserLocal != nil || info.Topic != nil {
		t.Fatalf("unexpected mute %+v or topic %v for a direct chat", info.UserLocal, info.Topic)
	}
}

func TestProjectChatInfoEmptyRosterKeepsDisplayMembersWithoutProfiles(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, makeUserLoginID(testSelfID))}}
	snap := chatSnapshot{
		data: chatmeta.ChatData{
			ChatID:            testChatID,
			Type:              "DirectChat",
			DisplayUserIDs:    []int64{testOtherID},
			DisplayNicknames:  []string{"Synthetic display name"},
			ActiveMemberCount: 5,
		},
	}

	info, err := kc.projectChatInfo(portal, snap)
	if err != nil {
		t.Fatal(err)
	}

	if info.Members.IsFull || info.Members.TotalMemberCount != 5 {
		t.Fatalf("members = %+v, want a partial list counting ActiveMemberCount", info.Members)
	}
	other, ok := info.Members.MemberMap[makeUserID(testOtherID)]
	if !ok || other.Membership != event.MembershipJoin || other.UserInfo != nil {
		t.Fatalf("display member = %+v, want joined without fabricated profile fields", other)
	}
	if info.Type != nil {
		t.Fatalf("room type = %v, want none for an incomplete roster", *info.Type)
	}
	if info.Name == nil || *info.Name != "Synthetic display name" {
		t.Fatalf("name = %v, want the display nickname fallback", info.Name)
	}
}

func TestProjectChatInfoGroupMapsMuteNameAndEmptyAnnouncement(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, makeUserLoginID(testSelfID))}}
	snap := chatSnapshot{
		data: chatmeta.ChatData{
			ChatID:       testChatID,
			Type:         "MultiChat",
			PushAlertSet: true,
			PushAlert:    false,
			ChatMetas:    []chatmeta.ChatMeta{{Type: chatmeta.SharedMetaTitle, Revision: 1, Content: "Synthetic group"}},
		},
		roster: []int64{testSelfID, testOtherID, 4000},
	}

	info, err := kc.projectChatInfo(portal, snap)
	if err != nil {
		t.Fatal(err)
	}

	if info.Name == nil || *info.Name != "Synthetic group" {
		t.Fatalf("name = %v, want Synthetic group", info.Name)
	}
	if info.UserLocal == nil || info.UserLocal.MutedUntil == nil || *info.UserLocal.MutedUntil != event.MutedForever {
		t.Fatalf("mute = %+v, want muted forever", info.UserLocal)
	}
	if info.Type != nil {
		t.Fatalf("room type = %v, want none for a group", *info.Type)
	}
	if !info.Members.IsFull || info.Members.TotalMemberCount != 3 {
		t.Fatalf("members = %+v, want three full members", info.Members)
	}
	if info.Topic == nil || *info.Topic != "" || info.ExtraUpdates == nil {
		t.Fatalf("topic = %v, want an empty topic with a revision update", info.Topic)
	}
}

func TestRosterMatches(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	expected := map[networkid.UserID]bool{makeUserID(testSelfID): true, makeUserID(testOtherID): true, makeUserID(4000): true}
	for _, test := range []struct {
		name        string
		roster      []int64
		allowSubset bool
		want        error
	}{
		{name: "exact roster", roster: []int64{testSelfID, testOtherID, 4000}},
		{name: "local account implied", roster: []int64{testOtherID, 4000}},
		{name: "duplicates collapse", roster: []int64{testSelfID, testOtherID, testOtherID, 4000}},
		{name: "empty roster", roster: nil, want: errRosterIncomplete},
		{name: "empty roster with subset", roster: nil, allowSubset: true, want: errRosterIncomplete},
		{name: "missing member", roster: []int64{testSelfID, testOtherID}, want: errRosterIncomplete},
		{name: "missing member with subset", roster: []int64{testSelfID, testOtherID}, allowSubset: true},
		{name: "unexpected member", roster: []int64{testSelfID, testOtherID, 6000}, want: errRosterUnexpected},
		{name: "unexpected member with subset", roster: []int64{testSelfID, 6000}, allowSubset: true, want: errRosterUnexpected},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := kc.rosterMatches(chatSnapshot{roster: test.roster}, expected, test.allowSubset)
			if !errors.Is(err, test.want) {
				t.Fatalf("rosterMatches = %v, want %v", err, test.want)
			}
		})
	}
}

func memberListCalls(f *fakeKakao) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, call := range f.metadataCalls {
		if strings.HasPrefix(call, "MemberList(") {
			count++
		}
	}
	return count
}

func TestRefreshGroupMembershipReadsMemberListOnce(t *testing.T) {
	ctx := context.Background()
	kc, backend, matrix := newGroupCreationFramework(t)
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	p.MXID = "!selected:test"
	if err = p.Save(ctx); err != nil {
		t.Fatal(err)
	}
	backend.chatInfo.ChatData.ActiveMemberCount = 2
	backend.memberList.MemberIDs = []int64{1000, 2000}
	matrix.members["@kakao_1000:test"] = &event.MemberEventContent{Membership: event.MembershipJoin}
	matrix.members["@kakao_2000:test"] = &event.MemberEventContent{Membership: event.MembershipJoin}
	matrix.members[id.UserID("@kakao_4000:test")] = &event.MemberEventContent{Membership: event.MembershipLeave}
	kc.queue = func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		return bridgev2.EventHandlingResult{Success: true}
	}

	if err = kc.refreshGroupMembership(ctx, backend, 5000, false); err != nil {
		t.Fatal(err)
	}

	if calls := memberListCalls(backend.fakeKakao); calls != 1 {
		t.Fatalf("MemberList calls = %d, want 1 per refresh", calls)
	}
}

func TestCreateGroupBindingReadsMemberListOnce(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	params := &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}

	response, err := kc.CreateGroup(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}

	if response.Portal.MXID != params.RoomID {
		t.Fatalf("bound room = %q, want %q", response.Portal.MXID, params.RoomID)
	}
	if calls := memberListCalls(backend.fakeKakao); calls != 1 {
		t.Fatalf("MemberList calls = %d, want 1 per binding", calls)
	}
}
