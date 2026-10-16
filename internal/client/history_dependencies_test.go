package client

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func attachDependencyPeer(t *testing.T, c *Client, backend *testloco.Backend) {
	t.Helper()
	c.dial = func(context.Context, authstate.State) (*session, error) {
		return testMetadataSession(backend, 1000), nil
	}
}
func TestHistoryDependenciesShareSourceAdmissionThroughRealClient(t *testing.T) {
	root := accountRoot(t)
	policy := HistoryPacing{MinimumInterval: 40 * time.Millisecond, ErrorBackoff: 100 * time.Millisecond}
	history := accountProfile(t, root, "history", 1000, policy)
	metadata := accountProfile(t, root, "metadata", 1000, policy)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	sourceBackend := newScriptedBackend(t, false, testloco.Expect("SYNCMSG", func(raw bson.Raw) error { close(entered); <-release; return checkSyncRequest(42, 100, 103)(raw) }, statusDocument(bson.E{Key: "chatLogs", Value: bson.A{bson.D{{Key: "logId", Value: int64(103)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "synthetic"}}}})))
	metadataDispatched := make(chan struct{})
	var metadataAt atomic.Int64
	metadataBackend := newScriptedBackend(t, false, testloco.Expect("CHATINFO", func(bson.Raw) error { metadataAt.Store(time.Now().UnixNano()); close(metadataDispatched); return nil }, statusDocument(bson.E{Key: "chatInfo", Value: bson.D{{Key: "c", Value: int64(42)}, {Key: "t", Value: "MultiChat"}}})), testloco.Expect("GETMSGS", nil, statusDocument(bson.E{Key: "chatLogs", Value: bson.A{bson.D{{Key: "chatId", Value: int64(42)}, {Key: "logId", Value: int64(103)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "synthetic current"}}}})))
	attachDependencyPeer(t, history, sourceBackend)
	attachDependencyPeer(t, metadata, metadataBackend)
	done := make(chan error, 2)
	go func() { _, err := history.ReadHistoryPage(t.Context(), 42, 100, 103, 3); done <- err }()
	<-entered
	go func() { _, err := metadata.ChatInfo(t.Context(), 42); done <- err }()
	waitHistoryDemands(t, metadata.historyAdmission, 1)
	select {
	case <-metadataDispatched:
		t.Fatal("metadata bypassed active account history")
	default:
	}
	once.Do(func() { close(release) })
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	got, err := metadata.GetMessages(t.Context(), 42, []int64{103})
	if err != nil || len(got) != 1 {
		t.Fatal(got, err)
	}
	if time.Since(time.Unix(0, metadataAt.Load())) < policy.MinimumInterval {
		t.Fatal("GETMSGS bypassed account spacing")
	}
	waitBackends(t, sourceBackend, metadataBackend)
}
func TestHistoryMalformedMetadataKeepsErrorCooldown(t *testing.T) {
	root := accountRoot(t)
	c := accountProfile(t, root, "metadata", 1000, HistoryPacing{MinimumInterval: time.Millisecond, ErrorBackoff: 200 * time.Millisecond})
	backend := newScriptedBackend(t, false, testloco.Expect("CHATINFO", nil, statusDocument(bson.E{Key: "chatInfo", Value: bson.D{{Key: "c", Value: int64(43)}, {Key: "t", Value: "MultiChat"}}})))
	attachDependencyPeer(t, c, backend)
	if _, err := c.ChatInfo(t.Context(), 42); !errors.Is(err, chatmeta.ErrInvalidResponse) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.GetMessages(ctx, 42, []int64{103}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("malformed response retired error cooldown", err)
	}
	waitBackends(t, backend)
}
func TestHistoryStoppedDependencyCannotReleaseProfileEarly(t *testing.T) {
	root := accountRoot(t)
	c := accountProfile(t, root, "metadata", 1000, HistoryPacing{MinimumInterval: time.Millisecond, ErrorBackoff: time.Millisecond})
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	done := make(chan error, 1)
	go func() {
		_, err := c.historyAdmission.exchange(t.Context(), "synthetic-dependency", func(context.Context) ([]byte, error) {
			close(entered)
			<-release
			return nil, errors.New("synthetic canceled dependency")
		})
		done <- err
	}()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := c.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("shutdown did not join dependency", err)
	}
	if lease, err := acquireProfileLease(filepath.Join(root, "metadata.lock")); !errors.Is(err, errProfileInUse) {
		if lease != nil {
			_ = lease.Close()
		}
		t.Fatal("profile ownership released before dependency publication", err)
	}
	once.Do(func() { close(release) })
	if err := <-done; err == nil {
		t.Fatal("failed dependency exchange reported success")
	}
	if err := c.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// The admission observer reports how long a paced metadata command waited for
// account spacing before it was sent.
func TestAdmissionObserverReportsPacedWait(t *testing.T) {
	root := accountRoot(t)
	policy := HistoryPacing{MinimumInterval: 80 * time.Millisecond, ErrorBackoff: 100 * time.Millisecond}
	c := accountProfile(t, root, "metadata", 1000, policy)
	info := statusDocument(bson.E{Key: "chatInfo", Value: bson.D{{Key: "c", Value: int64(42)}, {Key: "t", Value: "MultiChat"}}})
	backend := newScriptedBackend(t, false, testloco.Expect("CHATINFO", nil, info), testloco.Expect("CHATINFO", nil, info))
	attachDependencyPeer(t, c, backend)
	if _, err := c.ChatInfo(t.Context(), 42); err != nil {
		t.Fatal(err)
	}
	var command string
	var wait time.Duration
	ctx := WithAdmissionObserver(t.Context(), func(c string, d time.Duration) { command, wait = c, d })
	if _, err := c.ChatInfo(ctx, 42); err != nil {
		t.Fatal(err)
	}
	if command != "CHATINFO" || wait < policy.MinimumInterval/2 {
		t.Fatalf("observed %q wait %v, want CHATINFO near %v", command, wait, policy.MinimumInterval)
	}
	waitBackends(t, backend)
}
