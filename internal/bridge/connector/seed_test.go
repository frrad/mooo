package connector

import (
	"context"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
)

func syntheticLastLog(t *testing.T, logID, authorID int64, message string) bson.Raw {
	t.Helper()
	raw, err := bson.Marshal(bson.D{
		{Key: "logId", Value: logID},
		{Key: "authorId", Value: authorID},
		{Key: "type", Value: int32(1)},
		{Key: "message", Value: message},
		{Key: "sendAt", Value: int64(1760000000)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A fresh login previously left every chat empty: only regular groups got
// rooms, and catch-up excludes never-delivered chats. Each direct and regular
// group chat now shows the inventory's last message, a direct room is created
// from inventory data alone, and no history is requested.
func TestFreshLoginSeedsEachChatsLastMessageFromInventory(t *testing.T) {
	ctx := context.Background()
	kc, h := newDiscoveryFramework(t, testChatID, []int64{testSelfID, testOtherID})
	const directChatID, peerID = int64(4000), int64(4001)
	creationInfo := map[int64]*bridgev2.ChatInfo{}
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		if remote.GetType() == bridgev2.RemoteEventMessage {
			// Stand in for the framework: a message for a room that does not
			// exist yet asks the network for its creation info, and a
			// delivered message is stored.
			key := remote.GetPortalKey()
			chatID, err := parseChatID(key.ID)
			if err != nil {
				t.Fatal(err)
			}
			if chatID != testChatID {
				info, err := kc.GetChatInfo(ctx, &bridgev2.Portal{Portal: &database.Portal{PortalKey: key}})
				if err != nil {
					t.Fatalf("creation info for %d: %v", chatID, err)
				}
				creationInfo[chatID] = info
			}
			message := remote.(bridgev2.RemoteMessage)
			if err := kc.login.Bridge.DB.Message.Insert(ctx, &database.Message{
				ID: message.GetID(), MXID: id.EventID("$" + string(message.GetID()) + ":test"), Room: key,
				SenderID: message.GetSender().Sender, Timestamp: time.Unix(1760000000, 0),
				Metadata: &KakaoMessageMetadata{},
			}); err != nil {
				t.Fatal(err)
			}
		}
		return h.queue(remote)
	}
	f := &fakeKakao{
		stream:     make(chan events.Result),
		chatInfo:   chatmeta.ChatInfoResponse{ChatData: chatmeta.ChatData{ChatID: testChatID, Type: "MultiChat"}},
		memberList: chatmeta.MemberListResponse{MemberIDs: []int64{testSelfID, testOtherID}},
		chatInventory: []chatmeta.ChatData{
			{ChatID: testChatID, Type: "MultiChat", NewMessageCount: 1, LastChatLog: syntheticLastLog(t, 300, testOtherID, "group latest")},
			{
				ChatID: directChatID, Type: "DirectChat", NewMessageCount: 0,
				DisplayUserIDs: []int64{peerID}, DisplayNicknames: []string{"Synthetic Peer"},
				LastChatLog: syntheticLastLog(t, 400, peerID, "direct latest"),
			},
			{ChatID: 4100, Type: "DirectChat", Meta: &chatmeta.RoomMeta{ChatHide: true}, DisplayUserIDs: []int64{4101}, LastChatLog: syntheticLastLog(t, 410, 4101, "hidden")},
			{ChatID: 5000, Type: "MultiChat", LinkID: 1, LastChatLog: syntheticLastLog(t, 500, 5001, "open chat")},
			{ChatID: 6000, Type: "MemoChat", LastChatLog: syntheticLastLog(t, 600, testSelfID, "memo")},
		},
	}

	if _, err := kc.connectAndSubscribe(ctx, f); err != nil {
		t.Fatal(err)
	}

	var seeded []string
	var receipts []*simplevent.Receipt
	for _, remote := range h.queued {
		switch remote.GetType() {
		case bridgev2.RemoteEventMessage:
			seeded = append(seeded, string(remote.(bridgev2.RemoteMessage).GetID()))
		case bridgev2.RemoteEventReadReceipt:
			receipts = append(receipts, remote.(*simplevent.Receipt))
		}
	}
	if want := []string{"3000:300", "4000:400"}; !slices.Equal(seeded, want) {
		t.Fatalf("seeded messages = %v, want %v", seeded, want)
	}
	var committed []string
	for _, evt := range f.committed() {
		chatID, logID, _ := events.MessagePosition(evt)
		committed = append(committed, string(makeMessageID(chatID, logID)))
	}
	if want := []string{"3000:300", "4000:400"}; !slices.Equal(committed, want) {
		t.Fatalf("committed = %v, want %v", committed, want)
	}

	info := creationInfo[directChatID]
	if info == nil || info.Type == nil || *info.Type != database.RoomTypeDM || info.Members.OtherUserID != makeUserID(peerID) || !info.Members.IsFull {
		t.Fatalf("direct room creation info = %#v, want a DM with %d", info, peerID)
	}
	peer := info.Members.MemberMap[makeUserID(peerID)]
	if peer.UserInfo == nil || peer.UserInfo.Name == nil || *peer.UserInfo.Name != "Synthetic Peer" || peer.UserInfo.Avatar != nil {
		t.Fatalf("direct peer = %#v, want inventory nickname and no avatar change", peer.UserInfo)
	}
	for _, call := range f.metadataCalls {
		if call == "ChatInfo(4000)" || call == "MemberList(4000,0)" || call == "Members(4000,1)" {
			t.Fatalf("direct room creation requested %s; metadata calls = %v", call, f.metadataCalls)
		}
	}
	for _, call := range f.calls {
		if call == "CatchUp(4000,400)" || call == "CatchUp(3000,300)" {
			t.Fatalf("seeding requested history: %v", f.calls)
		}
	}

	// Only the chat the account had read is marked read, as the account.
	if len(receipts) != 1 || receipts[0].GetPortalKey().ID != "4000" || receipts[0].LastTarget != makeMessageID(directChatID, 400) || receipts[0].GetSender() != kc.selfSender() {
		t.Fatalf("receipts = %#v, want one own receipt at 4000:400", receipts)
	}

	// The inventory hold ends with seeding; later chat info is source-backed.
	if _, err := kc.inventoryChatInfo(nil, directChatID); err != nil {
		t.Fatal(err)
	}
	if held, _ := kc.inventoryChatInfo(nil, directChatID); held != nil {
		t.Fatal("direct chat is still held from inventory after seeding")
	}
}

func TestSeedingRunsBeforeCatchUpAndLiveSubscription(t *testing.T) {
	kc, _ := newTestClient(t, nil)
	f := &fakeKakao{
		stream: make(chan events.Result),
		chatInventory: []chatmeta.ChatData{{
			ChatID: 4000, Type: "DirectChat", DisplayUserIDs: []int64{4001},
			LastChatLog: syntheticLastLog(t, 400, 4001, "direct latest"),
		}},
	}

	if _, err := kc.connectAndSubscribe(context.Background(), f); err != nil {
		t.Fatal(err)
	}

	seed, resume := slices.Index(f.calls, "SeedLastMessage(4000)"), slices.Index(f.calls, "ResumeTargets")
	if seed < 0 || resume < 0 || seed > resume {
		t.Fatalf("calls = %v, want seeding before catch-up targets", f.calls)
	}
}

func TestSeedingSkipsChatsAlreadyDelivered(t *testing.T) {
	kc, h := newTestClient(t, nil)
	f := &fakeKakao{
		stream:        make(chan events.Result),
		seedDelivered: map[int64]bool{4000: true},
		chatInventory: []chatmeta.ChatData{{
			ChatID: 4000, Type: "DirectChat", DisplayUserIDs: []int64{4001},
			LastChatLog: syntheticLastLog(t, 400, 4001, "already bridged"),
		}},
	}

	if _, err := kc.connectAndSubscribe(context.Background(), f); err != nil {
		t.Fatal(err)
	}

	if h.queuedCount() != 0 || len(f.committed()) != 0 {
		t.Fatalf("queued %d and committed %d for a delivered chat, want none", h.queuedCount(), len(f.committed()))
	}
}

func TestUndeliveredSeedHoldsChat(t *testing.T) {
	kc, h := newTestClient(t, nil)
	h.results = []bridgev2.EventHandlingResult{bridgev2.EventHandlingResultFailed}
	f := &fakeKakao{
		stream: make(chan events.Result),
		chatInventory: []chatmeta.ChatData{{
			ChatID: 4000, Type: "DirectChat", DisplayUserIDs: []int64{4001},
			LastChatLog: syntheticLastLog(t, 400, 4001, "direct latest"),
		}},
	}

	_, err := kc.connectAndSubscribe(context.Background(), f)

	// The event pump retries the held seed before the chat's live commits,
	// without another login.
	if err != nil || len(f.committed()) != 0 || kc.holds.chats[4000] == nil {
		t.Fatalf("err = %v, commits = %d; want the undelivered seed held uncommitted", err, len(f.committed()))
	}
}

func TestMembershipFeedIsNotSeeded(t *testing.T) {
	kc, _ := newTestClient(t, nil)

	if kc.isSeedMessage(events.MemberAdded{ChatID: testChatID, LogID: 300, Members: []events.MemberIdentity{{UserID: testOtherID}}}) {
		t.Fatal("a membership feed was accepted as the seeded last message")
	}
	if !kc.isSeedMessage(events.TextMessage{ChatID: testChatID, LogID: 300, AuthorID: testOtherID, Message: "latest"}) {
		t.Fatal("a text message was rejected as the seeded last message")
	}
}

func TestInventoryDirectRoomNeedsExactlyOneOtherParticipant(t *testing.T) {
	cases := []struct {
		name string
		chat chatmeta.ChatData
		want bool
	}{
		{"one peer", chatmeta.ChatData{Type: "DirectChat", DisplayUserIDs: []int64{4001}}, true},
		{"no display member", chatmeta.ChatData{Type: "DirectChat"}, false},
		{"two display members", chatmeta.ChatData{Type: "DirectChat", DisplayUserIDs: []int64{4001, 4002}}, false},
		{"only self", chatmeta.ChatData{Type: "DirectChat", DisplayUserIDs: []int64{testSelfID}}, false},
		{"group", chatmeta.ChatData{Type: "MultiChat", DisplayUserIDs: []int64{4001}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inventoryDirectRoom(tc.chat, testSelfID); got != tc.want {
				t.Fatalf("inventoryDirectRoom = %t, want %t", got, tc.want)
			}
		})
	}
}
