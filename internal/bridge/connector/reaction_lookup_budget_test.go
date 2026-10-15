package connector

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/reactions"
)

// newReactionLookupFixture is a connector over a memory store holding
// bridged messages 1..messages in testChatID, a members backend whose roster
// is one heart from testOtherID, and a manual clock.
func newReactionLookupFixture(t *testing.T, messages int64) (*KakaoClient, *testHarness, *reactionTestBackend, *time.Time, *readAckTimers) {
	t.Helper()
	backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, members: reactions.MembersResponse{
		Revision: 1,
		Members:  map[reactions.Type][]int64{reactions.Heart: {testOtherID}},
		Fields:   map[string]json.RawMessage{"1": []byte(`[2000]`)},
	}}
	kc, harness := newTestClient(t, func() (kakaoClient, error) { return backend, nil })
	kc.client = backend
	clock := time.Unix(1_800_000_000, 0)
	kc.now = func() time.Time { return clock }
	timers := captureReadAckTimers(kc)
	for logID := int64(1); logID <= messages; logID++ {
		kc.store.(*memStore).putMessage(&database.Message{
			ID:       makeMessageID(testChatID, logID),
			MXID:     "$target:test",
			Room:     makePortalKey(testChatID, kc.login.ID),
			Metadata: &KakaoMessageMetadata{},
		})
	}
	return kc, harness, backend, &clock, timers
}

func TestReactionPushBurstOnOneMessageCoalescesLookups(t *testing.T) {
	kc, harness, backend, clock, timers := newReactionLookupFixture(t, 1)

	// Three people react to the same message within a second.
	for revision := int64(4); revision <= 6; revision++ {
		backend.members.Revision = revision
		if !kc.handleEvent(backend, events.ReactionChanged{ChatID: testChatID, LogID: 1, Revision: revision}) {
			t.Fatalf("push %d was not handled", revision)
		}
		*clock = clock.Add(300 * time.Millisecond)
	}
	// The first push reads at once; the rest wait for one trailing read.
	if backend.memberCalls != 1 {
		t.Fatalf("members reads during the burst = %d, want 1", backend.memberCalls)
	}
	if got := timers.armed(); got != 1 {
		t.Fatalf("armed trailing lookups = %d, want 1", got)
	}
	if timers.delays[0] <= 0 || timers.delays[0] > reactionLookupCoalesceWindow {
		t.Fatalf("trailing delay = %v, want within (0, %v]", timers.delays[0], reactionLookupCoalesceWindow)
	}
	*clock = clock.Add(reactionLookupCoalesceWindow)
	timers.fireAll()
	if backend.memberCalls != 2 {
		t.Fatalf("members reads after the burst = %d, want 2", backend.memberCalls)
	}
	stored, err := kc.store.firstMessagePart(context.Background(), kc.login.ID, makeMessageID(testChatID, 1))
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.Metadata.(*KakaoMessageMetadata).ReactionRevision; got != 6 {
		t.Fatalf("persisted revision = %d, want the trailing read's 6", got)
	}
	// The memory store keeps no reaction rows, so each applied read queues
	// the heart again: one per read, not one per push.
	if len(harness.queued) != 2 {
		t.Fatalf("queued = %d, want one per read", len(harness.queued))
	}
}

func TestReactionPushesOnDifferentMessagesAreNotCoalesced(t *testing.T) {
	kc, _, backend, _, timers := newReactionLookupFixture(t, 2)
	for logID := int64(1); logID <= 2; logID++ {
		if !kc.handleEvent(backend, events.ReactionChanged{ChatID: testChatID, LogID: logID, Revision: 1}) {
			t.Fatal("push was not handled")
		}
	}
	if backend.memberCalls != 2 || timers.armed() != 0 {
		t.Fatalf("members reads/timers = %d/%d, want 2/0", backend.memberCalls, timers.armed())
	}
}

func TestReactionLookupBudgetLeavesExcessPushesForViewingResync(t *testing.T) {
	kc, harness, backend, clock, _ := newReactionLookupFixture(t, reactionLookupBurst+1)
	for logID := int64(1); logID <= reactionLookupBurst; logID++ {
		if !kc.handleEvent(backend, events.ReactionChanged{ChatID: testChatID, LogID: logID, Revision: 1}) {
			t.Fatal("push within budget was not handled")
		}
	}
	if backend.memberCalls != reactionLookupBurst {
		t.Fatalf("members reads = %d, want the burst %d", backend.memberCalls, reactionLookupBurst)
	}
	queuedBefore := len(harness.queued)
	over := events.ReactionChanged{ChatID: testChatID, LogID: reactionLookupBurst + 1, Revision: 1}
	// Over budget the push is consumed without a read, a notice or a
	// persisted revision; viewing resync recovers it from the cursor.
	if !kc.handleEvent(backend, over) {
		t.Fatal("over-budget push ended delivery")
	}
	if backend.memberCalls != reactionLookupBurst {
		t.Fatalf("over-budget push read members: %d", backend.memberCalls)
	}
	if len(harness.queued) != queuedBefore {
		t.Fatalf("over-budget push queued %d events", len(harness.queued)-queuedBefore)
	}
	stored, err := kc.store.firstMessagePart(context.Background(), kc.login.ID, makeMessageID(testChatID, over.LogID))
	if err != nil {
		t.Fatal(err)
	}
	if got := stored.Metadata.(*KakaoMessageMetadata).ReactionRevision; got != 0 {
		t.Fatalf("over-budget push persisted revision %d", got)
	}
	// One token refills after the interval.
	*clock = clock.Add(reactionLookupRefill)
	if !kc.handleEvent(backend, over) {
		t.Fatal("refilled push was not handled")
	}
	if backend.memberCalls != reactionLookupBurst+1 {
		t.Fatalf("members reads after refill = %d, want %d", backend.memberCalls, reactionLookupBurst+1)
	}
}

func TestReactionLookupBudgetChargesMiniDetails(t *testing.T) {
	kc, _, backend, _, _ := newReactionLookupFixture(t, reactionLookupBurst)
	backend.members = reactions.MembersResponse{Revision: 0, Fields: map[string]json.RawMessage{"revision": []byte(`0`)}}
	backend.mini = reactions.DetailsResponse{Status: 0, Details: []reactions.Detail{}}
	// A mini push reads members and log details: two lookups.
	for logID := int64(1); logID <= reactionLookupBurst; logID++ {
		kc.handleEvent(backend, events.ReactionChanged{MetadataType: 2, ChatID: testChatID, LogID: logID, Revision: 1, Items: []events.ReactionItem{}})
	}
	if backend.memberCalls != reactionLookupBurst/2 {
		t.Fatalf("members reads = %d, want %d (two lookups per mini push)", backend.memberCalls, reactionLookupBurst/2)
	}
}

func TestReactionResyncStopsAtLookupBudgetAndKeepsCursor(t *testing.T) {
	kc, _, backend, _, _ := newReactionLookupFixture(t, reactionLookupBurst+2)
	backend.fakeKakao = reactionViewingBackend()
	backend.members.Revision = 1000
	var items []json.RawMessage
	for logID := int64(1); logID <= reactionLookupBurst+2; logID++ {
		items = append(items, syncedMeta(t, logID, 100+logID, `{"1":1}`))
	}
	backend.reactionPages = []reactions.SyncMetaPage{{Items: items, Last: true}}
	portal := &database.Portal{PortalKey: makePortalKey(testChatID, kc.login.ID), MXID: "!room:test"}
	err := kc.resyncChatReactions(context.Background(), backend, portal, testChatID)
	if !errors.Is(err, errReactionSyncBudget) {
		t.Fatalf("resync error = %v, want the retained-demand budget error", err)
	}
	if backend.memberCalls != reactionLookupBurst {
		t.Fatalf("members reads = %d, want %d", backend.memberCalls, reactionLookupBurst)
	}
	if raw, ok, _ := kc.store.get(context.Background(), reactionResyncKey(string(kc.login.ID), testChatID)); ok && raw != "" {
		t.Fatalf("cursor advanced to %q past unapplied rows", raw)
	}
}

// spacedReactionClock advances the connector's clock by one coalescing window
// on every read, so each push in a test that replays one change reads again.
func spacedReactionClock(kc *KakaoClient) {
	clock := time.Unix(1_800_000_000, 0)
	kc.now = func() time.Time {
		clock = clock.Add(reactionLookupCoalesceWindow)
		return clock
	}
}
