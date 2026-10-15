package connector

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
)

// A member's Matrix receipt only moves forward: an older or repeated
// watermark, including one delivered after a restart, is not bridged.
func TestMemberReadReceiptsNeverMoveBackwards(t *testing.T) {
	f := newReadReceiptFramework(t)
	first := f.bridgeText(t, 100)
	second := f.bridgeText(t, 102)
	for _, watermark := range []int64{102, 100, 102} {
		if !f.kc.handleEvent(&fakeKakao{}, events.ReadStateChanged{ChatID: testChatID, UserID: testOtherID, Watermark: watermark}) {
			t.Fatalf("DECUNREAD %d was not handled", watermark)
		}
	}
	want := []readReceiptMark{{room: "!room:test", eventID: second}}
	if got := f.matrix.ghost.markedEvents(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ghost receipts = %v, want only the forward receipt %v (not %s)", got, want, first)
	}
	// A new connection with the same bridge database keeps the watermark.
	reopened := newKakaoClient(f.login, newBridgeStore(f.login.Bridge.DB), testSelfID, nil)
	reopened.queue = f.kc.queue
	if !reopened.handleEvent(&fakeKakao{}, events.ReadStateChanged{ChatID: testChatID, UserID: testOtherID, Watermark: 100}) {
		t.Fatal("stale DECUNREAD after restart was not handled")
	}
	if got := f.matrix.ghost.markedEvents(); len(got) != 1 {
		t.Fatalf("stale receipt after restart moved the member back: %v", got)
	}
	// Each member's watermark is independent: the account's own lower
	// watermark is still bridged for the Matrix user.
	if !reopened.handleEvent(&fakeKakao{}, events.ReadStateChanged{ChatID: testChatID, UserID: testSelfID, Watermark: 100}) {
		t.Fatal("own DECUNREAD was not handled")
	}
	if got := f.matrix.user.markedEvents(); len(got) != 1 || got[0].eventID != first {
		t.Fatalf("own receipt = %v, want %s", got, first)
	}
}

// Members who read while the bridge was offline are recovered when the room
// is next viewed, from CHATONROOM's watermarks, forward-only per member. The
// official client sends CHATONROOM on chat entry, never as a connect sweep
// (research/bridge/METADATA-TRIGGERS.md).
func TestViewingRecoversMemberReadWatermarks(t *testing.T) {
	f := newReadReceiptFramework(t)
	first := f.bridgeText(t, 100)
	second := f.bridgeText(t, 102)
	backend := &fakeKakao{chatInfo: chatmeta.ChatInfoResponse{ChatData: chatmeta.ChatData{ChatID: testChatID, Type: "DirectChat"}}, chatOnRoom: map[int64]chatmeta.ChatOnRoomResponse{
		testChatID: {ChatID: testChatID, Full: true, Watermarks: map[int64]int64{testOtherID: 102, testSelfID: 100}},
	}}
	f.kc.client = backend
	portal, err := f.bridge.GetExistingPortalByKey(context.Background(), makePortalKey(testChatID, f.login.ID))
	if err != nil || portal == nil {
		t.Fatalf("portal: %v", err)
	}
	view := func() {
		t.Helper()
		f.kc.mu.Lock()
		f.kc.reactionView, f.kc.reactionSyncNext = nil, time.Time{}
		f.kc.mu.Unlock()
		_, _ = f.kc.syncRoomReactions(context.Background(), portal)
	}
	view()
	if len(backend.chatOnRoomCalls) != 1 || backend.chatOnRoomCalls[0] != testChatID {
		t.Fatalf("CHATONROOM calls = %v", backend.chatOnRoomCalls)
	}
	if got := f.matrix.ghost.markedEvents(); len(got) != 1 || got[0].eventID != second {
		t.Fatalf("member receipt = %v, want %s", got, second)
	}
	if got := f.matrix.user.markedEvents(); len(got) != 1 || got[0].eventID != first {
		t.Fatalf("own receipt = %v, want %s", got, first)
	}
	// A later snapshot with a lower watermark never moves a member back.
	backend.chatOnRoom[testChatID] = chatmeta.ChatOnRoomResponse{ChatID: testChatID, Full: true, Watermarks: map[int64]int64{testOtherID: 100}}
	view()
	if got := f.matrix.ghost.markedEvents(); len(got) != 1 {
		t.Fatalf("lower snapshot moved a member back: %v", got)
	}
}
