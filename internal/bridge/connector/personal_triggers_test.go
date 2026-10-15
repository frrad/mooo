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

func personalReads(backend *countingGroupCreateClient) int {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	n := 0
	for _, call := range backend.metadataCalls {
		if strings.HasPrefix(call, "PersonalMetas(") {
			n++
		}
	}
	return n
}

func newPersonalGroup(t *testing.T) (*KakaoClient, *countingGroupCreateClient, *bridgev2.Portal) {
	t.Helper()
	kc, backend, _ := newGroupCreationFramework(t)
	backend.personalMeta = &chatmeta.RoomMeta{Name: "Synthetic personal"}
	response, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}})
	if err != nil {
		t.Fatal(err)
	}
	drainMetadataRefresh(t, kc, backend)
	return kc, backend, response.Portal
}

// The official client sends GETMCMETA after login only when the login-list
// mcmRevision exceeds the revision it last applied, never by age.
func TestPersonalMetaFollowsLoginRevision(t *testing.T) {
	kc, backend, _ := newPersonalGroup(t)
	base := personalReads(backend)
	later := kc.now().Add(24 * time.Hour)
	kc.now = func() time.Time { return later }
	login := func(revision int32) int {
		t.Helper()
		backend.mu.Lock()
		backend.mcmRevision = &revision
		backend.mu.Unlock()
		kc.personalRevisionDemand(t.Context(), backend)
		drainMetadataRefresh(t, kc, backend)
		return personalReads(backend) - base
	}
	if n := login(5); n != 1 {
		t.Fatalf("first login revision: %d reads, want 1", n)
	}
	if n := login(5); n != 1 {
		t.Fatalf("unchanged revision reread personal metadata: %d reads", n)
	}
	if n := login(4); n != 1 {
		t.Fatalf("older revision reread personal metadata: %d reads", n)
	}
	if n := login(6); n != 2 {
		t.Fatalf("newer revision did not reread: %d reads", n)
	}
	snap := kc.metadataRefresh.rooms()[0].snapshot
	cache, err := kc.readRoomMetadata(t.Context(), 5000)
	if err != nil {
		t.Fatal(err)
	}
	kc.enqueueEnrichment(snap, cache)
	drainMetadataRefresh(t, kc, backend)
	if n := personalReads(backend) - base; n != 2 {
		t.Fatalf("a day-old checkpoint was reread by age: %d reads", n)
	}
}

// CHGMCMETA carries the new value; the official client applies it and its
// revision without GETMCMETA.
func TestPersonalMetaPushIsAppliedWithoutGETMCMETA(t *testing.T) {
	kc, backend, portal := newPersonalGroup(t)
	base := personalReads(backend)
	resync, ok := kc.remoteEventFor(events.ChatMCMetaChanged{ChatID: 5000, Revision: 9, Type: "name", Content: "Synthetic renamed"}).(*simplevent.ChatResync)
	if !ok {
		t.Fatal("CHGMCMETA did not produce a room resync")
	}
	if _, err := resync.GetChatInfoFunc(t.Context(), portal); err != nil {
		t.Fatal(err)
	}
	drainMetadataRefresh(t, kc, backend)
	if n := personalReads(backend) - base; n != 0 {
		t.Fatalf("CHGMCMETA refetched personal metadata: %d reads", n)
	}
	cache, err := kc.readRoomMetadata(t.Context(), 5000)
	if err != nil || cache.Personal == nil || cache.Personal.Name != "Synthetic renamed" {
		t.Fatalf("pushed name not stored: %+v %v", cache.Personal, err)
	}
	revision := int32(9)
	backend.mcmRevision = &revision
	kc.personalRevisionDemand(t.Context(), backend)
	drainMetadataRefresh(t, kc, backend)
	if n := personalReads(backend) - base; n != 0 {
		t.Fatalf("login after an applied push reread personal metadata: %d reads", n)
	}
}
