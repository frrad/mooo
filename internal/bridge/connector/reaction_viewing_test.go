package connector

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/reactions"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

func TestViewingReactionEligibilityAndStopFlag(t *testing.T) {
	for _, tc := range []struct {
		typ  string
		stop bool
		want int
	}{
		{"DirectChat", false, 1}, {"MultiChat", false, 1}, {"OD", false, 1}, {"OM", false, 1},
		{"PlusChat", false, 0}, {"MemoChat", false, 0}, {"PartnerChat", false, 0}, {"", false, 0}, {"FutureChat", false, 0}, {"MultiChat", true, 0},
	} {
		t.Run(tc.typ+map[bool]string{true: "-stopped", false: ""}[tc.stop], func(t *testing.T) {
			kc, portal, _, _ := newFrameworkReactionFixture(t, &recordingIntent{})
			backend := reactionViewingBackend()
			backend.chatInfo.ChatData.Type = tc.typ
			backend.chatOnRoom[testChatID] = chatmeta.ChatOnRoomResponse{ChatID: testChatID, StopSyncLogMeta: tc.stop}
			kc.client = backend
			if err := openReactionView(kc, portal); err != nil {
				t.Fatal(err)
			}
			if len(backend.reactionCursors) != tc.want {
				t.Fatalf("metadata requests=%v, want %d", backend.reactionCursors, tc.want)
			}
			if tc.want == 0 && !tc.stop && len(backend.chatOnRoomCalls) != 0 {
				t.Fatal("incompatible chat entered")
			}
			for range 6 { // repeat heartbeats for an unchanged/disabled open room
				previous := kc.now()
				kc.now = func() time.Time { return previous.Add(5 * time.Second) }
				if err := kc.HandleMatrixViewingChat(context.Background(), &bridgev2.MatrixViewingChat{Portal: portal}); err != nil {
					t.Fatal(err)
				}
			}
			if len(backend.reactionCursors) != tc.want || len(backend.metadataCalls) != 1 {
				t.Fatal("heartbeat repeated source lookup or empty sync")
			}
		})
	}
}

func TestViewingReactionFirstTickAndNewRevision(t *testing.T) {
	kc, portal, _, _ := newFrameworkReactionFixture(t, &recordingIntent{})
	backend := reactionViewingBackend()
	kc.client = backend
	now := time.Unix(100, 0)
	kc.now = func() time.Time { return now }
	msg := &bridgev2.MatrixViewingChat{Portal: portal}
	for range 2 {
		if err := kc.HandleMatrixViewingChat(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
		if len(backend.metadataCalls) != 0 || len(backend.reactionCursors) != 0 {
			t.Fatal("source requested before first ten-second tick")
		}
		now = now.Add(5 * time.Second)
	}
	if err := kc.HandleMatrixViewingChat(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if len(backend.reactionCursors) != 1 {
		t.Fatal("first viewing sync missing")
	}
	// Equal revision does not cause another request. A newer known revision does.
	kc.observeReactionRevision(testChatID, 0)
	now = now.Add(10 * time.Second)
	if err := kc.HandleMatrixViewingChat(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if len(backend.reactionCursors) != 1 {
		t.Fatal("unchanged room polled")
	}
	kc.observeReactionRevision(testChatID, 500)
	if err := kc.HandleMatrixViewingChat(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if len(backend.reactionCursors) != 2 || len(backend.metadataCalls) != 1 || len(backend.chatOnRoomCalls) != 1 {
		t.Fatal("dirty sync or cached eligibility wrong")
	}
	// An empty response has not reconciled the known revision: another tick may retry.
	now = now.Add(5 * time.Second)
	if err := kc.HandleMatrixViewingChat(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if len(backend.reactionCursors) != 2 {
		t.Fatal("dirty sync bypassed interval")
	}
}

func TestViewingReactionFailureMismatchAndHeartbeatExpiry(t *testing.T) {
	for _, stage := range []string{"chat-info-error", "chat-info-mismatch", "entry-error", "entry-mismatch", "sync-error"} {
		t.Run(stage, func(t *testing.T) {
			kc, portal, _, _ := newFrameworkReactionFixture(t, &recordingIntent{})
			backend := reactionViewingBackend()
			switch stage {
			case "chat-info-error":
				backend.chatInfoErr = errors.New("synthetic secret")
			case "chat-info-mismatch":
				backend.chatInfo.ChatData.ChatID++
			case "entry-error":
				backend.chatOnRoomErr = errors.New("synthetic secret")
			case "entry-mismatch":
				backend.chatOnRoom[testChatID] = chatmeta.ChatOnRoomResponse{ChatID: 4000}
			case "sync-error":
				backend.reactionSyncErr = errors.New("synthetic secret")
			}
			kc.client = backend
			if err := openReactionView(kc, portal); !errors.Is(err, errReactionResync) {
				t.Fatalf("error=%v", err)
			}
			if stage != "sync-error" && len(backend.reactionCursors) != 0 {
				t.Fatal("lookup failure reached metadata sync")
			}
			backend.chatInfoErr = nil
			backend.chatInfo.ChatData.ChatID = testChatID
			backend.chatOnRoomErr = nil
			backend.chatOnRoom[testChatID] = chatmeta.ChatOnRoomResponse{ChatID: testChatID}
			backend.reactionSyncErr = nil
			now := kc.now().Add(reactionViewingExpiry)
			kc.now = func() time.Time { return now }
			msg := &bridgev2.MatrixViewingChat{Portal: portal}
			before := len(backend.reactionCursors)
			if err := kc.HandleMatrixViewingChat(context.Background(), msg); err != nil {
				t.Fatal(err)
			}
			if len(backend.reactionCursors) != before {
				t.Fatal("expired viewing episode synced immediately")
			}
			now = now.Add(reactionViewingInterval)
			if err := kc.HandleMatrixViewingChat(context.Background(), msg); err != nil {
				t.Fatal(err)
			}
			if len(backend.reactionCursors) != before+1 {
				t.Fatal("new viewing episode did not recover")
			}
		})
	}
}

type heldReactionSync struct {
	*fakeKakao
	entered  chan struct{}
	released chan struct{}
}

func (b *heldReactionSync) React(context.Context, reactions.Request) (reactions.Response, error) {
	return reactions.Response{}, nil
}

func (b *heldReactionSync) ReactionMetaSync(ctx context.Context, _ int64, _ int64) (reactions.SyncMetaPage, error) {
	close(b.entered)
	select {
	case <-ctx.Done():
		return reactions.SyncMetaPage{}, ctx.Err()
	case <-b.released:
		return reactions.SyncMetaPage{Last: true}, nil
	}
}

func TestViewingReactionCoalescingAndSwitchCancellation(t *testing.T) {
	kc, portal, _, _ := newFrameworkReactionFixture(t, &recordingIntent{})
	backend := &heldReactionSync{fakeKakao: reactionViewingBackend(), entered: make(chan struct{}), released: make(chan struct{})}
	kc.client = backend
	now := time.Unix(100, 0)
	kc.now = func() time.Time { return now }
	msg := &bridgev2.MatrixViewingChat{Portal: portal}
	if err := kc.HandleMatrixViewingChat(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Second)
	done := make(chan error, 1)
	go func() { done <- kc.HandleMatrixViewingChat(context.Background(), msg) }()
	select {
	case <-backend.entered:
	case <-time.After(time.Second):
		t.Fatal("request not entered")
	}
	if err := kc.HandleMatrixViewingChat(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if len(backend.metadataCalls) != 1 {
		t.Fatal("concurrent heartbeat repeated request")
	}
	if err := kc.HandleMatrixViewingChat(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, errReactionResync) {
			t.Fatalf("cancel=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("switch did not cancel sync")
	}
	kc.mu.Lock()
	defer kc.mu.Unlock()
	if kc.reactionView != nil || kc.reactionSyncBusy {
		t.Fatal("old request retained viewing ownership")
	}
}

func TestReconnectDoesNotSweepReactionMetadata(t *testing.T) {
	kc, _, _, _ := newFrameworkReactionFixture(t, &recordingIntent{})
	backend := reactionViewingBackend()
	backend.stream = make(chan events.Result)
	if _, err := kc.connectAndSubscribe(context.Background(), backend); err != nil {
		t.Fatal(err)
	}
	if len(backend.reactionCursors) != 0 {
		t.Fatal("reconnect swept metadata without viewing")
	}
}

type heldReactionMembers struct {
	*fakeKakao
	entered chan struct{}
}

func (b *heldReactionMembers) ReactionMembers(ctx context.Context, _, _ int64) (reactions.MembersResponse, error) {
	close(b.entered)
	<-ctx.Done()
	return reactions.MembersResponse{}, ctx.Err()
}

func TestViewingCancellationReachesReactionDetails(t *testing.T) {
	kc, portal, _, _ := newFrameworkReactionFixture(t, &recordingIntent{})
	backend := &heldReactionMembers{fakeKakao: reactionViewingBackend(), entered: make(chan struct{})}
	backend.reactionPages = []reactions.SyncMetaPage{{Items: []json.RawMessage{syncedMeta(t, 99, 500, `{"2":1}`)}, Last: true}}
	kc.client = backend
	now := time.Unix(100, 0)
	kc.now = func() time.Time { return now }
	msg := &bridgev2.MatrixViewingChat{Portal: portal}
	if err := kc.HandleMatrixViewingChat(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Second)
	done := make(chan error, 1)
	go func() { done <- kc.HandleMatrixViewingChat(context.Background(), msg) }()
	select {
	case <-backend.entered:
	case <-time.After(time.Second):
		t.Fatal("details not entered")
	}
	if err := kc.HandleMatrixViewingChat(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, errReactionResync) {
			t.Fatalf("cancel=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("switch did not cancel details")
	}
	if raw, _, _ := kc.store.get(context.Background(), reactionResyncKey(string(kc.login.ID), testChatID)); raw != "" {
		t.Fatal("canceled details advanced cursor")
	}
}

type failedReactionCursorStore struct {
	store
	failRead, failWrite bool
}

func (s *failedReactionCursorStore) get(ctx context.Context, key string) (string, bool, error) {
	if s.failRead {
		return "", false, errors.New("synthetic read failure")
	}
	return s.store.get(ctx, key)
}
func (s *failedReactionCursorStore) put(ctx context.Context, key, value string) error {
	if s.failWrite {
		return errors.New("synthetic write failure")
	}
	return s.store.put(ctx, key, value)
}

func TestViewingCursorFailureKeepsDemandPending(t *testing.T) {
	for _, read := range []bool{true, false} {
		t.Run(map[bool]string{true: "read", false: "write"}[read], func(t *testing.T) {
			kc, portal, _, _ := newFrameworkReactionFixture(t, &recordingIntent{})
			backend := reactionViewingBackend()
			// This target is unbridged, so advancing its metadata cursor needs no detail request.
			backend.reactionPages = []reactions.SyncMetaPage{{Items: []json.RawMessage{syncedMeta(t, 1234, 500, `{"2":1}`)}, Last: true}}
			kc.client = backend
			st := &failedReactionCursorStore{store: kc.store, failRead: read, failWrite: !read}
			kc.store = st
			if err := openReactionView(kc, portal); !errors.Is(err, errReactionResync) {
				t.Fatalf("failure=%v", err)
			}
			if !kc.reactionView.pending {
				t.Fatal("cursor failure discarded pending demand")
			}
			st.failRead = false
			st.failWrite = false
			if raw, _, _ := kc.store.get(context.Background(), reactionResyncKey(string(kc.login.ID), testChatID)); raw != "" {
				t.Fatal("failed cursor write persisted")
			}
		})
	}
}

func TestViewingPageBudgetContinuesOnLaterTick(t *testing.T) {
	kc, portal, _, _ := newFrameworkReactionFixture(t, &recordingIntent{})
	backend := reactionViewingBackend()
	for i := range reactionResyncPages + 1 {
		backend.reactionPages = append(backend.reactionPages, reactions.SyncMetaPage{Items: []json.RawMessage{syncedMeta(t, 1234, int64(500+i), `{"2":1}`)}, Last: i == reactionResyncPages})
	}
	kc.client = backend
	if err := openReactionView(kc, portal); err != nil {
		t.Fatal(err)
	}
	if len(backend.reactionCursors) != reactionResyncPages || !kc.reactionView.pending {
		t.Fatal("page budget lost continuation")
	}
	now := kc.now().Add(5 * time.Second)
	kc.now = func() time.Time { return now }
	if err := kc.HandleMatrixViewingChat(context.Background(), &bridgev2.MatrixViewingChat{Portal: portal}); err != nil {
		t.Fatal(err)
	}
	if len(backend.reactionCursors) != reactionResyncPages+1 || kc.reactionView.pending {
		t.Fatal("later viewing tick did not complete")
	}
	if got := backend.reactionCursors[reactionResyncPages]; got != 509 {
		t.Fatalf("resumed cursor=%d", got)
	}
}

func TestViewingDisconnectCancelsSync(t *testing.T) {
	kc, portal, _, _ := newFrameworkReactionFixture(t, &recordingIntent{})
	backend := &heldReactionSync{fakeKakao: reactionViewingBackend(), entered: make(chan struct{}), released: make(chan struct{})}
	kc.client = backend
	now := time.Unix(100, 0)
	kc.now = func() time.Time { return now }
	msg := &bridgev2.MatrixViewingChat{Portal: portal}
	if err := kc.HandleMatrixViewingChat(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Second)
	done := make(chan error, 1)
	go func() { done <- kc.HandleMatrixViewingChat(context.Background(), msg) }()
	select {
	case <-backend.entered:
	case <-time.After(time.Second):
		t.Fatal("request not entered")
	}
	kc.Disconnect()
	select {
	case err := <-done:
		if !errors.Is(err, errReactionResync) {
			t.Fatalf("cancel=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect did not cancel")
	}
	if kc.reactionView != nil || kc.client != nil {
		t.Fatal("disconnected view retained")
	}
}

func TestOwnReactionDuringSyncKeepsRefreshDemand(t *testing.T) {
	kc, portal, login, _ := newFrameworkReactionFixture(t, &recordingIntent{})
	backend := &heldReactionSync{fakeKakao: reactionViewingBackend(), entered: make(chan struct{}), released: make(chan struct{})}
	kc.client = backend
	now := time.Unix(100, 0)
	kc.now = func() time.Time { return now }
	viewing := &bridgev2.MatrixViewingChat{Portal: portal}
	if err := kc.HandleMatrixViewingChat(context.Background(), viewing); err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Second)
	done := make(chan error, 1)
	go func() { done <- kc.HandleMatrixViewingChat(context.Background(), viewing) }()
	select {
	case <-backend.entered:
	case <-time.After(time.Second):
		t.Fatal("request not entered")
	}
	content := &event.ReactionEventContent{RelatesTo: event.RelatesTo{Key: "❤️"}}
	target, err := kc.store.firstMessagePart(context.Background(), login.ID, makeMessageID(testChatID, 99))
	if err != nil || target == nil {
		t.Fatal("target unavailable")
	}
	msg := &bridgev2.MatrixReaction{MatrixEventBase: bridgev2.MatrixEventBase[*event.ReactionEventContent]{Portal: portal, Event: &event.Event{Sender: login.UserMXID}, Content: content}, TargetMessage: target}
	if _, err := kc.HandleMatrixReaction(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	close(backend.released)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("sync did not finish")
	}
	if !kc.reactionView.pending {
		t.Fatal("completed sync discarded concurrent own reaction refresh")
	}
}
