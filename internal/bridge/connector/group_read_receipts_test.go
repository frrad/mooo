package connector

import (
	"context"
	"errors"
	"fmt"
	"testing"

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
	reopened := newKakaoClient(f.login, testSelfID, nil)
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

// chatOnRoomKakao serves CHATONROOM watermarks for the recovery path.
type chatOnRoomKakao struct {
	*fakeKakao
	responses map[int64]chatmeta.ChatOnRoomResponse
	calls     []int64
	err       error
}

func (c *chatOnRoomKakao) ChatOnRoom(_ context.Context, chatID int64) (chatmeta.ChatOnRoomResponse, error) {
	c.calls = append(c.calls, chatID)
	if c.err != nil {
		return chatmeta.ChatOnRoomResponse{}, c.err
	}
	return c.responses[chatID], nil
}

// Members who read while the bridge was offline are recovered on the next
// connection from CHATONROOM's watermarks, forward-only per member.
func TestReconnectRecoversMemberReadWatermarks(t *testing.T) {
	f := newReadReceiptFramework(t)
	first := f.bridgeText(t, 100)
	second := f.bridgeText(t, 102)
	backend := &chatOnRoomKakao{fakeKakao: &fakeKakao{}, responses: map[int64]chatmeta.ChatOnRoomResponse{
		testChatID: {ChatID: testChatID, Full: true, Watermarks: map[int64]int64{testOtherID: 102, testSelfID: 100}},
	}}
	f.kc.recoverReadWatermarks(context.Background(), backend)
	if len(backend.calls) != 1 || backend.calls[0] != testChatID {
		t.Fatalf("CHATONROOM calls = %v", backend.calls)
	}
	if got := f.matrix.ghost.markedEvents(); len(got) != 1 || got[0].eventID != second {
		t.Fatalf("member receipt = %v, want %s", got, second)
	}
	if got := f.matrix.user.markedEvents(); len(got) != 1 || got[0].eventID != first {
		t.Fatalf("own receipt = %v, want %s", got, first)
	}
	// A later snapshot with a lower watermark never moves a member back.
	backend.responses[testChatID] = chatmeta.ChatOnRoomResponse{ChatID: testChatID, Full: true, Watermarks: map[int64]int64{testOtherID: 100}}
	f.kc.recoverReadWatermarks(context.Background(), backend)
	if got := f.matrix.ghost.markedEvents(); len(got) != 1 {
		t.Fatalf("lower snapshot moved a member back: %v", got)
	}
	// A failed request is skipped without blocking the connection.
	backend.err = errors.New("synthetic chat-on failure")
	f.kc.recoverReadWatermarks(context.Background(), backend)
}
