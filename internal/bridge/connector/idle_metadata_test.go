package connector

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/frrad/mooo/internal/protocol/events"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

// The official Mac client has no MEMBER, Boards or personal-metadata timer
// (research/bridge/METADATA-TRIGGERS.md). An idle connected session must not
// read source metadata, however long it runs and however stale the profiles.
func TestIdleSessionSendsNoMetadataRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		kc, backend, _ := newGroupCreationFramework(t)
		ctx := context.Background()
		if _, err := kc.CreateGroup(ctx, &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}); err != nil {
			t.Fatal(err)
		}
		drainMetadataRefresh(t, kc, backend)
		later := kc.now().Add(24 * time.Hour)
		kc.now = func() time.Time { return later }
		backend.mu.Lock()
		backend.metadataCalls = nil
		backend.mu.Unlock()

		kc.wait = func(ctx context.Context, _ time.Duration) error { <-ctx.Done(); return ctx.Err() }
		stream := make(chan events.Result)
		done := make(chan struct{})
		go func() {
			kc.run(backend, stream, make(chan struct{}), kc.generation)
			close(done)
		}()
		time.Sleep(30 * time.Minute)
		synctest.Wait()
		backend.mu.Lock()
		calls := append([]string(nil), backend.metadataCalls...)
		backend.mu.Unlock()
		close(stream)
		<-done
		kc.Disconnect()
		synctest.Wait()
		if len(calls) != 0 {
			t.Fatalf("idle session read source metadata: %v", calls)
		}
	})
}
