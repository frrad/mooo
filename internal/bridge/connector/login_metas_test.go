package connector

import (
	"slices"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
)

func metaReads(backend *countingGroupCreateClient) []string {
	var reads []string
	for _, call := range sourceCalls(backend) {
		if strings.HasPrefix(call, "GetMetas(") || strings.HasPrefix(call, "ChatInfo(") {
			reads = append(reads, call)
		}
	}
	return reads
}

// Mac 26.8.0 (static): after the last LCHATLIST page the client sends
// GETMETAS, at most 50 chat IDs each, for rooms whose chat-data
// metaMaxRevision exceeds the stored room's meta revision, and merges each
// returned meta into the room when it is newer for its type. A group renamed
// while the bridge was offline is renamed in Matrix from GETMETAS alone; an
// unchanged revision sends nothing on the next login.
func TestLoginReadsMetasForRoomsWithNewerRevision(t *testing.T) {
	kc, backend := newManagedGroupReconnect(t)
	matrix := kc.login.Bridge.Matrix.(*groupCreationMatrix)
	backend.mu.Lock()
	renamed := backend.chatInventory[0]
	renamed.ChatMetas, renamed.MetaMaxRevision = nil, 9
	backend.chatInventory = []chatmeta.ChatData{renamed}
	backend.metaSets = map[int64]chatmeta.ChatMetaSet{5000: {ChatID: 5000, Metas: []chatmeta.ChatMeta{{Type: chatmeta.SharedMetaTitle, Revision: 9, Content: "Renamed offline"}}}}
	backend.mu.Unlock()
	if _, err := kc.connectAndSubscribe(t.Context(), backend); err != nil {
		t.Fatal(err)
	}
	drainMetadataRefresh(t, kc, backend)
	if reads := metaReads(backend); !slices.Equal(reads, []string{"GetMetas([5000])"}) {
		t.Fatalf("login meta reads = %v, want one GETMETAS", reads)
	}
	matrix.mu.Lock()
	name := matrix.name
	matrix.mu.Unlock()
	if name != "Renamed offline" {
		t.Fatalf("Matrix room name = %q", name)
	}

	backend.mu.Lock()
	backend.metadataCalls = nil
	backend.stream = make(chan events.Result)
	backend.mu.Unlock()
	if _, err := kc.connectAndSubscribe(t.Context(), backend); err != nil {
		t.Fatal(err)
	}
	drainMetadataRefresh(t, kc, backend)
	if reads := metaReads(backend); len(reads) != 0 {
		t.Fatalf("unchanged revision re-read metas: %v", reads)
	}
}
