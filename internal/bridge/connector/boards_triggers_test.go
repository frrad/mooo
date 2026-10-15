package connector

import (
	"strings"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
)

func moimReads(backend *countingGroupCreateClient) int {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	n := 0
	for _, call := range backend.metadataCalls {
		if strings.HasPrefix(call, "MoimMeta(") {
			n++
		}
	}
	return n
}

func newBoardsGroup(t *testing.T) (*KakaoClient, *countingGroupCreateClient, *bridgev2.Portal, *[]*bridgev2.ChatInfo) {
	t.Helper()
	kc, backend, _ := newGroupCreationFramework(t)
	backend.moimResponse = chatmeta.MoimResponse{ChatID: 5000, Metas: []chatmeta.MoimMeta{{Type: chatmeta.MoimMetaNotice, UpdateRevision: 40, Content: `{"type":"TEXT","notice":true,"content":"Synthetic first"}`}}}
	response, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}})
	if err != nil {
		t.Fatal(err)
	}
	var published []*bridgev2.ChatInfo
	kc.queue = func(evt bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		if change, ok := evt.(*simplevent.ChatInfoChange); ok && change.ChatInfoChange != nil {
			published = append(published, change.ChatInfoChange.ChatInfo)
		}
		return bridgev2.EventHandlingResultSuccess
	}
	drainMetadataRefresh(t, kc, backend)
	if moimReads(backend) != 1 {
		t.Fatalf("new group did not read its Boards once: %v", backend.metadataCalls)
	}
	return kc, backend, response.Portal, &published
}

// The official client merges a CHGMOMETAS payload into its stored Boards and
// never refetches for it (research/bridge/METADATA-TRIGGERS.md).
func TestBoardsPushPayloadIsAppliedWithoutGETMOMETA(t *testing.T) {
	kc, backend, portal, published := newBoardsGroup(t)
	push := events.ChatMoimMetaChanged{ChatID: 5000, Metas: []chatmeta.MoimMeta{{Type: chatmeta.MoimMetaNotice, UpdateRevision: 41, Content: `{"type":"TEXT","notice":true,"content":"Synthetic pushed"}`}}}
	resync, ok := kc.remoteEventFor(push).(*simplevent.ChatResync)
	if !ok {
		t.Fatal("CHGMOMETAS did not produce a room resync")
	}
	if _, err := resync.GetChatInfoFunc(t.Context(), portal); err != nil {
		t.Fatal(err)
	}
	drainMetadataRefresh(t, kc, backend)
	if n := moimReads(backend); n != 1 {
		t.Fatalf("CHGMOMETAS refetched Boards: %d reads", n)
	}
	cache, err := kc.readRoomMetadata(t.Context(), 5000)
	if err != nil || len(cache.Boards) != 1 || cache.Boards[0].UpdateRevision != 41 {
		t.Fatalf("push payload not stored: %+v %v", cache.Boards, err)
	}
	last := (*published)[len(*published)-1]
	if last.Topic == nil || *last.Topic != "Synthetic pushed" {
		t.Fatalf("pushed announcement not projected: %+v", last.Topic)
	}
	// A push without the notice type, or with an older revision, keeps it.
	for _, metas := range [][]chatmeta.MoimMeta{nil, {{Type: chatmeta.MoimMetaNotice, UpdateRevision: 39, Content: `{}`}}} {
		resync = kc.remoteEventFor(events.ChatMoimMetaChanged{ChatID: 5000, Metas: metas}).(*simplevent.ChatResync)
		if _, err = resync.GetChatInfoFunc(t.Context(), portal); err != nil {
			t.Fatal(err)
		}
		drainMetadataRefresh(t, kc, backend)
		cache, _ = kc.readRoomMetadata(t.Context(), 5000)
		if len(cache.Boards) != 1 || cache.Boards[0].UpdateRevision != 41 {
			t.Fatalf("push %+v replaced newer Boards: %+v", metas, cache.Boards)
		}
	}
}

// Boards never expire on a timer: neither a new snapshot nor a cached room
// read of an old checkpoint rereads them.
func TestStoredBoardsAreNotRefetchedByAge(t *testing.T) {
	kc, backend, portal, _ := newBoardsGroup(t)
	later := kc.now().Add(24 * time.Hour)
	kc.now = func() time.Time { return later }
	if _, _, err := kc.cachedChatInfo(t.Context(), portal, false); err != nil {
		t.Fatal(err)
	}
	snap := kc.metadataRefresh.rooms()[0].snapshot
	cache, err := kc.readRoomMetadata(t.Context(), 5000)
	if err != nil {
		t.Fatal(err)
	}
	kc.enqueueEnrichment(snap, cache)
	drainMetadataRefresh(t, kc, backend)
	if n := moimReads(backend); n != 1 {
		t.Fatalf("stored Boards were refetched: %d reads", n)
	}
}

// Native GETMOMETA follows chat entry: CHATONROOM reports Boards revisions and
// only a type whose revision differs from the stored one is reread. The
// bridge's chat entry is the Matrix viewing demand.
func TestViewingRereadsBoardsOnlyWhenRevisionChanged(t *testing.T) {
	kc, backend, portal, _ := newBoardsGroup(t)
	view := func(revision int64) int {
		t.Helper()
		backend.mu.Lock()
		backend.chatOnRoom = map[int64]chatmeta.ChatOnRoomResponse{5000: {ChatID: 5000, MoimRevisions: map[int32]int64{chatmeta.MoimMetaNotice: revision}}}
		backend.mu.Unlock()
		kc.mu.Lock()
		kc.reactionView, kc.reactionSyncNext = nil, time.Time{}
		kc.mu.Unlock()
		_, _ = kc.syncRoomReactions(t.Context(), portal)
		drainMetadataRefresh(t, kc, backend)
		return moimReads(backend)
	}
	if n := view(40); n != 1 {
		t.Fatalf("unchanged revision reread Boards: %d reads", n)
	}
	backend.moimResponse.Metas[0].UpdateRevision = 42
	if n := view(42); n != 2 {
		t.Fatalf("changed revision did not reread Boards: %d reads", n)
	}
	if n := view(42); n != 2 {
		t.Fatalf("reread repeated for an applied revision: %d reads", n)
	}
}
