package connector

import (
	"fmt"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func rosterReads(backend *countingGroupCreateClient) int {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	n := 0
	for _, call := range backend.metadataCalls {
		if strings.HasPrefix(call, "MemberList(") {
			n++
		}
	}
	return n
}

// newManagedGroupReconnect creates a managed regular group, then prepares a
// reconnect whose login inventory lists it. The official client reads no roster
// in the background; membership follows feeds (research/bridge/METADATA-TRIGGERS.md).
func newManagedGroupReconnect(t *testing.T) (*KakaoClient, *countingGroupCreateClient) {
	t.Helper()
	kc, backend, _ := newGroupCreationFramework(t)
	if _, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}); err != nil {
		t.Fatal(err)
	}
	drainMetadataRefresh(t, kc, backend)
	backend.mu.Lock()
	backend.metadataCalls = nil
	backend.chatInventory = []chatmeta.ChatData{backend.chatInfo.ChatData}
	backend.stream = make(chan events.Result)
	backend.mu.Unlock()
	return kc, backend
}

func TestReconnectWithoutMembershipFeedsReadsNoRoster(t *testing.T) {
	kc, backend := newManagedGroupReconnect(t)
	if _, err := kc.connectAndSubscribe(t.Context(), backend); err != nil {
		t.Fatal(err)
	}
	if n := rosterReads(backend); n != 0 {
		t.Fatalf("ordinary reconnect read the roster %d times: %v", n, backend.metadataCalls)
	}
}

// Observed 2026-10-14: an offline leave or invite reaches catch-up as a type-0
// membership feed. It takes the same authoritative path as a DELMEM/NEWMEM push
// and is committed, so the next reconnect does not replay it.
func TestCatchUpMembershipFeedRefreshesRosterAndCommits(t *testing.T) {
	for _, feed := range []events.Event{
		events.MemberRemoved{ChatID: 5000, LogID: 200, UserID: 4000},
		events.MemberAdded{ChatID: 5000, LogID: 200, Members: []events.MemberIdentity{{UserID: 4000}}},
	} {
		t.Run(fmt.Sprintf("%T", feed), func(t *testing.T) {
			kc, backend := newManagedGroupReconnect(t)
			backend.resumeTargets = []syncmsg.Target{{ChatID: 5000, MaxLogID: 200}}
			backend.catchUps = map[int64]catchUpResult{5000: {events: []events.Event{feed}}}
			if _, err := kc.connectAndSubscribe(t.Context(), backend); err != nil {
				t.Fatal(err)
			}
			if n := rosterReads(backend); n != 1 {
				t.Fatalf("membership feed roster reads = %d, want 1", n)
			}
			committed := false
			for _, evt := range backend.commits {
				if _, logID, ok := events.MessagePosition(evt); ok && logID == 200 {
					committed = true
				}
			}
			if !committed {
				t.Fatalf("membership feed was not committed: %v", backend.commits)
			}
		})
	}
}

// A chat whose catch-up interval could not be recovered may have lost a
// membership feed, so its roster is read as the documented fallback.
func TestCatchUpGapFallsBackToRosterRead(t *testing.T) {
	kc, backend := newManagedGroupReconnect(t)
	backend.resumeTargets = []syncmsg.Target{{ChatID: 5000, MaxLogID: 200}}
	backend.catchUps = map[int64]catchUpResult{5000: {err: client.ErrGapUnresolved}}
	if _, err := kc.connectAndSubscribe(t.Context(), backend); err != nil {
		t.Fatal(err)
	}
	if n := rosterReads(backend); n != 1 {
		t.Fatalf("gap roster reads = %d, want 1", n)
	}
}

// A managed group this profile never delivered a message from is not caught
// up, so its offline membership changes are unrecovered: read the roster.
func TestUndeliveredManagedGroupFallsBackToRosterRead(t *testing.T) {
	kc, backend := newManagedGroupReconnect(t)
	backend.undeliveredTargets = []syncmsg.Target{{ChatID: 5000, MaxLogID: 200}}
	if _, err := kc.connectAndSubscribe(t.Context(), backend); err != nil {
		t.Fatal(err)
	}
	if n := rosterReads(backend); n != 1 {
		t.Fatalf("undelivered group roster reads = %d, want 1", n)
	}
}
