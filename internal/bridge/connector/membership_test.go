package connector

import (
	"context"
	"errors"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
)

func TestRemoteMemberEventsRefreshSourceMetadata(t *testing.T) {
	fake := &fakeKakao{chatInfo: chatmeta.ChatInfoResponse{ChatData: chatmeta.ChatData{ChatID: testChatID}}}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	kc.client = fake
	cases := []events.Event{
		events.MemberAdded{ChatID: testChatID},
		events.MemberRemoved{ChatID: testChatID},
		events.ChatStatusChanged{ChatID: testChatID},
		events.ChatMetaChanged{ChatID: testChatID},
	}
	for _, input := range cases {
		evt := kc.remoteEventFor(input)
		if member, ok := evt.(*chatInfoChangeEvent); ok {
			if member.GetType() != bridgev2.RemoteEventChatInfoChange || member.GetPortalKey().ID != "3000" || member.getInfo == nil || member.getChanges == nil {
				t.Fatalf("member refresh metadata = %+v", member.EventMeta)
			}
			if _, err := member.GetChatInfoChange(context.Background()); err != nil {
				t.Fatalf("source metadata refresh failed: %v", err)
			}
		} else if resync, ok := evt.(*simplevent.ChatResync); !ok || resync.GetType() != bridgev2.RemoteEventChatResync || resync.GetPortalKey().ID != "3000" || resync.GetChatInfoFunc == nil {
			t.Fatalf("%T mapped to unexpected refresh event", evt)
		}
	}
}

func TestRemoteChatLeftChangesOnlyLoggedInMembership(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	evt := kc.remoteEventFor(events.ChatLeft{ChatID: testChatID})
	change, ok := evt.(*simplevent.ChatInfoChange)
	if !ok {
		t.Fatalf("left mapped to %T, want ChatInfoChange", evt)
	}
	if change.ChatInfoChange == nil || change.ChatInfoChange.ChatInfo != nil {
		t.Fatal("left event unexpectedly carried room/profile deletion data")
	}
	members := change.ChatInfoChange.MemberChanges.MemberMap
	member, ok := members[makeUserID(testSelfID)]
	if !ok || member.Membership != event.MembershipLeave || !member.IsFromMe {
		t.Fatalf("left membership = %+v, want logged-in user leave", member)
	}
}

func TestMemberRemovalOfCreatingAccountIsNotDiscarded(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	changes, err := kc.memberChanges(context.Background(), testChatID, []events.MemberIdentity{{UserID: testSelfID}}, false)
	if err != nil {
		t.Fatal(err)
	}
	member, present := changes.MemberMap[makeUserID(testSelfID)]
	if !present || member.Membership != event.MembershipLeave || !member.IsFromMe {
		t.Fatalf("bridge-account removal lost: %+v", member)
	}
}

func TestPartialRosterMembershipDeltasRemainExplicit(t *testing.T) {
	fake := &fakeKakao{
		chatInfo: chatmeta.ChatInfoResponse{ChatData: chatmeta.ChatData{ChatID: testChatID, DisplayUserIDs: []int64{testOtherID}, ActiveMemberCount: 3}},
		members:  []chatmeta.Member{{UserID: testOtherID, Nickname: "existing"}, {UserID: 4000, Nickname: "invitee"}},
	}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	kc.client = fake
	added := kc.remoteEventFor(events.MemberAdded{ChatID: testChatID, Members: []events.MemberIdentity{{UserID: 4000}, {UserID: 0}, {UserID: testSelfID}}}).(*chatInfoChangeEvent)
	change, err := added.GetChatInfoChange(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if change.ChatInfo == nil || change.ChatInfo.Members.IsFull {
		t.Fatal("partial source roster was marked complete")
	}
	member, ok := change.MemberChanges.MemberMap[makeUserID(4000)]
	if !ok || member.Membership != event.MembershipJoin || member.UserInfo == nil || member.UserInfo.Name == nil {
		t.Fatalf("added member delta = %+v", member)
	}

	removed := kc.remoteEventFor(events.MemberRemoved{ChatID: testChatID, UserID: testOtherID}).(*chatInfoChangeEvent)
	change, err = removed.GetChatInfoChange(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	member, ok = change.MemberChanges.MemberMap[makeUserID(testOtherID)]
	if !ok || member.Membership != event.MembershipLeave {
		t.Fatalf("removed member delta = %+v", member)
	}

	added = kc.remoteEventFor(events.MemberAdded{ChatID: testChatID, Members: []events.MemberIdentity{{UserID: 5000}}}).(*chatInfoChangeEvent)
	change, err = added.GetChatInfoChange(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	member, ok = change.MemberChanges.MemberMap[makeUserID(5000)]
	if !ok || member.Membership != event.MembershipJoin || member.UserInfo != nil {
		t.Fatalf("omitted profile join = %+v, want identity-only join", member)
	}
}

func TestMembershipJoinLookupFailurePreservesIdentity(t *testing.T) {
	fake := &fakeKakao{membersErr: errors.New("profile lookup unavailable")}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	kc.client = fake
	changes, err := kc.memberChanges(context.Background(), testChatID, []events.MemberIdentity{{UserID: 4000}}, true)
	if err != nil {
		t.Fatal(err)
	}
	member, ok := changes.MemberMap[makeUserID(4000)]
	if !ok || member.Membership != event.MembershipJoin || member.UserInfo != nil {
		t.Fatalf("lookup failure join = %+v, want identity-only join", member)
	}
}

func TestMembershipRefreshRejectsForeignChatMetadata(t *testing.T) {
	fake := &fakeKakao{chatInfo: chatmeta.ChatInfoResponse{ChatData: chatmeta.ChatData{ChatID: testChatID + 1}}}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	kc.client = fake
	evt := kc.remoteEventFor(events.MemberRemoved{ChatID: testChatID, UserID: testOtherID}).(*chatInfoChangeEvent)
	if _, err := evt.GetChatInfoChange(context.Background()); !errors.Is(err, errChatInfoMismatch) {
		t.Fatalf("foreign chat refresh error = %v, want chat ID mismatch", err)
	}
}

func TestChatResyncRejectsForeignBoundPortal(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	resync := kc.remoteEventFor(events.ChatStatusChanged{ChatID: testChatID}).(*simplevent.ChatResync)
	foreign := &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID+1, makeUserLoginID(testSelfID))}}
	if _, err := resync.GetChatInfoFunc(context.Background(), foreign); !errors.Is(err, errChatInfoMismatch) {
		t.Fatalf("foreign resync error = %v, want chat ID mismatch", err)
	}
}
