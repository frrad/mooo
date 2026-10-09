package connector

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
)

func (f *fakeKakao) ListChats(context.Context) ([]chatmeta.ChatData, error) {
	return f.chatInventory, f.chatInventoryErr
}

func TestBootstrapDiscoversRegularGroupsBeforeLiveSubscription(t *testing.T) {
	kc, h := newTestClient(t, nil)
	f := &fakeKakao{memberList: chatmeta.MemberListResponse{MemberIDs: []int64{testSelfID, testOtherID}}, chatInfo: chatmeta.ChatInfoResponse{ChatData: chatmeta.ChatData{ChatID: testChatID, Type: "MultiChat"}}, stream: make(chan events.Result), chatInventory: []chatmeta.ChatData{{ChatID: testChatID, Type: "MultiChat"}, {ChatID: 4000, Type: "DirectChat"}, {ChatID: 5000, Type: "MultiChat", LinkID: 1}}}
	if _, err := kc.connectAndSubscribe(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if len(h.queued) != 1 {
		t.Fatalf("queued %d events, want one regular group discovery", len(h.queued))
	}
	evt, ok := h.queued[0].(*simplevent.ChatResync)
	if !ok || !evt.ShouldCreatePortal() || evt.GetPortalKey().ID != "3000" {
		t.Fatalf("discovery event = %#v", h.queued[0])
	}
}

func TestNewMemberDiscoveryMayCreatePortalButLeaveMayNot(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	for _, join := range []bool{true, false} {
		evt := kc.memberChange(testChatID, 42, testOtherID, nil, join)
		if join {
			f := &fakeKakao{memberList: chatmeta.MemberListResponse{MemberIDs: []int64{testSelfID, testOtherID}}, chatInfo: chatmeta.ChatInfoResponse{ChatData: chatmeta.ChatData{ChatID: testChatID, Type: "MultiChat"}}}
			if err := kc.prepareMemberDiscovery(context.Background(), f, testChatID, evt.(*chatInfoChangeEvent)); err != nil {
				t.Fatal(err)
			}
		}
		creator := evt.(bridgev2.RemoteEventThatMayCreatePortal)
		if creator.ShouldCreatePortal() != join {
			t.Fatalf("join=%t create=%t", join, creator.ShouldCreatePortal())
		}
	}
}

func TestDiscoveryFailureStopsBeforeEventsAndDoesNotCreatePortal(t *testing.T) {
	for _, mode := range []string{"incomplete-list", "wrong-room", "unsupported-room", "missing-roster", "matrix-failure"} {
		t.Run(mode, func(t *testing.T) {
			kc, h := newTestClient(t, nil)
			f := &fakeKakao{stream: make(chan events.Result), chatInventory: []chatmeta.ChatData{{ChatID: testChatID, Type: "MultiChat"}}, chatInfo: chatmeta.ChatInfoResponse{ChatData: chatmeta.ChatData{ChatID: testChatID, Type: "MultiChat"}}, memberList: chatmeta.MemberListResponse{MemberIDs: []int64{testSelfID, testOtherID}}}
			switch mode {
			case "incomplete-list":
				f.chatInventoryErr = client.ErrChatListIncomplete
			case "wrong-room":
				f.chatInfo.ChatData.ChatID = 4000
			case "unsupported-room":
				f.chatInfo.ChatData.Type = "SecretChat"
			case "missing-roster":
				f.memberList.MemberIDs = nil
			case "matrix-failure":
				h.results = []bridgev2.EventHandlingResult{bridgev2.EventHandlingResultFailed}
			}
			if _, err := kc.connectAndSubscribe(context.Background(), f); err == nil {
				t.Fatal("failed discovery subscribed")
			}
			for _, call := range f.calls {
				if call == "Events" {
					t.Fatal("subscribed after failure")
				}
			}
			if mode != "matrix-failure" && len(h.queued) != 0 {
				t.Fatal("queued creation without validated metadata")
			}
		})
	}
}

func TestDiscoveryDeduplicatesInventoryAndPreservesSourceRoster(t *testing.T) {
	kc, h := newTestClient(t, nil)
	f := &fakeKakao{chatInventory: []chatmeta.ChatData{{ChatID: testChatID, Type: "MultiChat"}, {ChatID: testChatID, Type: "MultiChat"}}, chatInfo: chatmeta.ChatInfoResponse{ChatData: chatmeta.ChatData{ChatID: testChatID, Type: "MultiChat", ChatMetas: []chatmeta.ChatMeta{{Type: chatmeta.SharedMetaTitle, Content: "Synthetic group"}}}}, memberList: chatmeta.MemberListResponse{MemberIDs: []int64{testSelfID, testOtherID, 4000}}}
	if err := kc.discoverGroups(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if len(h.queued) != 1 {
		t.Fatalf("events=%d", len(h.queued))
	}
	evt := h.queued[0].(*simplevent.ChatResync)
	p := &bridgev2.Portal{Portal: &database.Portal{PortalKey: evt.PortalKey}}
	info, err := evt.GetChatInfo(context.Background(), p)
	if err != nil || info.Name == nil || *info.Name != "Synthetic group" || !info.Members.IsFull || len(info.Members.MemberMap) != 3 {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	if len(f.committed()) != 0 || len(f.marks) != 0 {
		t.Fatal("discovery changed messages or read state")
	}
	p.ID = "4000"
	if _, err := evt.GetChatInfo(context.Background(), p); err == nil {
		t.Fatal("accepted a foreign portal")
	}
}

func TestObservedGroupDiscoveryFixtureUsesProductionBootstrap(t *testing.T) {
	data, err := os.ReadFile("../../../research/fixtures/bridge/group-discovery-observed.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name            string              `json:"name"`
			Inventory       []chatmeta.ChatData `json:"inventory"`
			ChatData        chatmeta.ChatData   `json:"chat_data"`
			MemberIDs       []int64             `json:"member_ids"`
			Members         []chatmeta.Member   `json:"members"`
			Portals         int                 `json:"expected_portals"`
			ExpectedName    string              `json:"expected_name"`
			ExpectedMembers []int64             `json:"expected_member_ids"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 2 {
		t.Fatal("fixture cases missing")
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			kc, h := newTestClient(t, nil)
			f := &fakeKakao{stream: make(chan events.Result), chatInventory: tc.Inventory, chatInfo: chatmeta.ChatInfoResponse{ChatData: tc.ChatData}, memberList: chatmeta.MemberListResponse{MemberIDs: tc.MemberIDs}, members: tc.Members}
			if _, err := kc.connectAndSubscribe(context.Background(), f); err != nil {
				t.Fatal(err)
			}
			if len(h.queued) != tc.Portals {
				t.Fatalf("portals=%d want=%d", len(h.queued), tc.Portals)
			}
			if tc.Portals == 0 {
				return
			}
			evt := h.queued[0].(*simplevent.ChatResync)
			portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: evt.PortalKey}}
			info, err := evt.GetChatInfo(context.Background(), portal)
			if err != nil {
				t.Fatal(err)
			}
			if info.Name == nil || *info.Name != tc.ExpectedName || !info.Members.IsFull || len(info.Members.MemberMap) != len(tc.ExpectedMembers) {
				t.Fatalf("info=%+v", info)
			}
			for _, memberID := range tc.ExpectedMembers {
				if _, ok := info.Members.MemberMap[networkid.UserID(strconv.FormatInt(memberID, 10))]; !ok {
					t.Fatalf("missing source identity %d", memberID)
				}
			}
		})
	}
}
