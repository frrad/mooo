package connector

import (
	"slices"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
)

// newManagedFeedGroup creates managed group 5000 with roster {1000, 2000,
// 4000}, all joined in Matrix, and clears the recorded source requests.
func newManagedFeedGroup(t *testing.T) (*KakaoClient, *countingGroupCreateClient, *groupCreationMatrix) {
	t.Helper()
	kc, backend, matrix := newGroupCreationFramework(t)
	backend.members = []chatmeta.Member{{UserID: 2000, Nickname: "Two"}, {UserID: 4000, Nickname: "Four"}}
	if _, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}); err != nil {
		t.Fatal(err)
	}
	drainMetadataRefresh(t, kc, backend)
	if roster := kc.verifiedRoster(t.Context(), 5000); !slices.Equal(roster, []int64{1000, 2000, 4000}) {
		t.Fatalf("created roster = %v", roster)
	}
	backend.mu.Lock()
	backend.metadataCalls = nil
	backend.mu.Unlock()
	return kc, backend, matrix
}

func sourceCalls(backend *countingGroupCreateClient) []string {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return slices.Clone(backend.metadataCalls)
}

func matrixMembership(matrix *groupCreationMatrix, mxid id.UserID) event.Membership {
	matrix.mu.Lock()
	defer matrix.mu.Unlock()
	if member := matrix.members[mxid]; member != nil {
		return member.Membership
	}
	return ""
}

// The official client's DELMEM consumer removes the leaver from the stored
// room with no request (research/bridge/METADATA-TRIGGERS.md).
func TestDelMemFeedRemovesLeaverFromStoredRosterWithoutRequests(t *testing.T) {
	kc, backend, matrix := newManagedFeedGroup(t)
	if !kc.handleEvent(backend, events.MemberRemoved{ChatID: 5000, LogID: 200, UserID: 4000}) {
		t.Fatal("leave feed was not handled")
	}
	if calls := sourceCalls(backend); len(calls) != 0 {
		t.Fatalf("leave feed sent source requests: %v", calls)
	}
	if got := matrixMembership(matrix, "@kakao_4000:test"); got != event.MembershipLeave {
		t.Fatalf("departed ghost membership = %q, want leave", got)
	}
	if roster := kc.verifiedRoster(t.Context(), 5000); !slices.Equal(roster, []int64{1000, 2000}) {
		t.Fatalf("roster after leave = %v", roster)
	}
	if err := kc.checkSourceAccess(t.Context(), 5000); err != nil {
		t.Fatalf("converged leave left the group paused: %v", err)
	}
	drainMetadataRefresh(t, kc, backend)
	if calls := sourceCalls(backend); len(calls) != 0 {
		t.Fatalf("leave feed queued source requests: %v", calls)
	}
}

// A replayed feed that the stored roster already reflects changes nothing.
func TestReplayedDelMemFeedIsIdempotent(t *testing.T) {
	kc, backend, matrix := newManagedFeedGroup(t)
	for logID := int64(200); logID <= 201; logID++ {
		if !kc.handleEvent(backend, events.MemberRemoved{ChatID: 5000, LogID: logID, UserID: 4000}) {
			t.Fatalf("leave feed %d was not handled", logID)
		}
	}
	if calls := sourceCalls(backend); len(calls) != 0 {
		t.Fatalf("replayed leave sent source requests: %v", calls)
	}
	if got := matrixMembership(matrix, "@kakao_4000:test"); got != event.MembershipLeave {
		t.Fatalf("departed ghost membership = %q, want leave", got)
	}
}

// The official client's NEWMEM consumer, for a stored room that does not
// invite the account itself, adds the feed's invitees and sends MEMBER for
// them. CHATINFO is reserved for an unknown room or a self invite.
func TestNewMemFeedAddsInviteesAndSendsOnlyMember(t *testing.T) {
	kc, backend, matrix := newManagedFeedGroup(t)
	backend.members = []chatmeta.Member{{UserID: 6000, Nickname: "Six"}}
	if !kc.handleEvent(backend, events.MemberAdded{ChatID: 5000, LogID: 200, Members: []events.MemberIdentity{{UserID: 6000}}}) {
		t.Fatal("invite feed was not handled")
	}
	if calls := sourceCalls(backend); len(calls) != 0 {
		t.Fatalf("invite feed sent synchronous source requests: %v", calls)
	}
	if got := matrixMembership(matrix, "@kakao_6000:test"); got != event.MembershipJoin {
		t.Fatalf("invitee ghost membership = %q, want join", got)
	}
	if roster := kc.verifiedRoster(t.Context(), 5000); !slices.Equal(roster, []int64{1000, 2000, 4000, 6000}) {
		t.Fatalf("roster after invite = %v", roster)
	}
	if err := kc.checkSourceAccess(t.Context(), 5000); err != nil {
		t.Fatalf("converged invite left the group paused: %v", err)
	}
	drainMetadataRefresh(t, kc, backend)
	if calls := sourceCalls(backend); !slices.Equal(calls, []string{"Members(5000,1)"}) {
		t.Fatalf("invite feed source requests = %v, want one MEMBER", calls)
	}
	ghost, err := kc.login.Bridge.GetGhostByID(t.Context(), makeUserID(6000))
	if err != nil || ghost.Name != "Six" {
		t.Fatalf("invitee profile was not applied: %v", err)
	}
}

// NEWMEM sends MEMBER for every invitee, including one whose profile the
// client already stores (a member who left and was invited back).
func TestNewMemFeedRefetchesKnownInviteeProfile(t *testing.T) {
	kc, backend, _ := newManagedFeedGroup(t)
	if !kc.handleEvent(backend, events.MemberRemoved{ChatID: 5000, LogID: 200, UserID: 4000}) {
		t.Fatal("leave feed was not handled")
	}
	later := time.Now().Add(time.Hour)
	kc.now = func() time.Time { return later }
	backend.members = []chatmeta.Member{{UserID: 4000, Nickname: "Four again"}}
	if !kc.handleEvent(backend, events.MemberAdded{ChatID: 5000, LogID: 201, Members: []events.MemberIdentity{{UserID: 4000}}}) {
		t.Fatal("invite feed was not handled")
	}
	drainMetadataRefresh(t, kc, backend)
	if calls := sourceCalls(backend); !slices.Equal(calls, []string{"Members(5000,1)"}) {
		t.Fatalf("re-invite source requests = %v, want one MEMBER", calls)
	}
	ghost, err := kc.login.Bridge.GetGhostByID(t.Context(), makeUserID(4000))
	if err != nil || ghost.Name != "Four again" {
		t.Fatalf("re-invited profile was not refreshed: %v", err)
	}
}

// A feed that invites the account itself is the native CHATINFO case; the
// bridge keeps its authoritative roster read for it.
func TestNewMemFeedInvitingSelfReadsRoster(t *testing.T) {
	kc, backend, _ := newManagedFeedGroup(t)
	if !kc.handleEvent(backend, events.MemberAdded{ChatID: 5000, LogID: 200, Members: []events.MemberIdentity{{UserID: testSelfID}}}) {
		t.Fatal("self invite feed was not handled")
	}
	if n := rosterReads(backend); n != 1 {
		t.Fatalf("self invite roster reads = %d, want 1: %v", n, sourceCalls(backend))
	}
}

// A group with no verified roster (here: paused by an earlier failure) cannot
// apply a delta, so it reads the roster.
func TestMembershipFeedForUnverifiedGroupReadsRoster(t *testing.T) {
	kc, backend, _ := newManagedFeedGroup(t)
	p, err := kc.login.Bridge.GetPortalByKey(t.Context(), makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err = kc.saveMembershipCheckpoint(t.Context(), p, true, nil); err != nil {
		t.Fatal(err)
	}
	if !kc.handleEvent(backend, events.MemberRemoved{ChatID: 5000, LogID: 200, UserID: 4000}) {
		t.Fatal("leave feed was not handled")
	}
	if n := rosterReads(backend); n != 1 {
		t.Fatalf("unverified group roster reads = %d, want 1: %v", n, sourceCalls(backend))
	}
}
