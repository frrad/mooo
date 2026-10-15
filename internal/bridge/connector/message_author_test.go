package connector

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func memberRequests(backend *countingGroupCreateClient) []string {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	var out []string
	for _, call := range backend.metadataCalls {
		if strings.HasPrefix(call, "Members(") {
			out = append(out, call)
		}
	}
	return out
}

// The official client's message conversion sends MEMBER for an author it has
// no record of, or whose stored nickname is empty
// (research/bridge/METADATA-TRIGGERS.md). A known author costs nothing.
func TestMessageFromUnknownAuthorFetchesItsProfile(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	now := time.Now().UTC()
	kc.now = func() time.Time { return now }
	backend.members = []chatmeta.Member{{UserID: 2000, Nickname: "Known"}, {UserID: 4000, Nickname: ""}}
	if _, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}); err != nil {
		t.Fatal(err)
	}
	drainMetadataRefresh(t, kc, backend)
	backend.mu.Lock()
	backend.metadataCalls = nil
	backend.members = []chatmeta.Member{{UserID: 6000, Nickname: "Newcomer"}, {UserID: 4000, Nickname: "Named later"}}
	backend.mu.Unlock()
	message := func(author int64) []string {
		t.Helper()
		kc.remoteEventFor(events.TextMessage{ChatID: 5000, LogID: 100 + author, AuthorID: author, Message: "synthetic"})
		drainMetadataRefresh(t, kc, backend)
		return memberRequests(backend)
	}
	if calls := message(2000); len(calls) != 0 {
		t.Fatalf("known author fetched: %v", calls)
	}
	if calls := message(6000); len(calls) != 1 || calls[0] != fmt.Sprintf("Members(%d,1)", 5000) {
		t.Fatalf("unknown author not fetched once: %v", calls)
	}
	if calls := message(6000); len(calls) != 1 {
		t.Fatalf("stored author refetched: %v", calls)
	}
	now = now.Add(memberProfileAttemptCooldown)
	if calls := message(4000); len(calls) != 2 {
		t.Fatalf("author with an empty stored nickname not refetched: %v", calls)
	}
	record, _, err := kc.readMemberProfile(t.Context(), 4000)
	if err != nil || record.Profile == nil || record.Profile.Nickname != "Named later" {
		t.Fatalf("refetched nickname not stored: %+v %v", record.Profile, err)
	}
	if calls := message(4000); len(calls) != 2 {
		t.Fatalf("named author refetched: %v", calls)
	}
}
