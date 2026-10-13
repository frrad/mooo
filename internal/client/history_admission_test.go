package client

import (
	"context"
	"errors"
	"github.com/frrad/mooo/internal/authstate"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/privatejson"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func admissionTestResponse(t *testing.T, id int64) syncmsg.Response {
	t.Helper()
	raw, err := bson.Marshal(bson.D{{Key: "logId", Value: id}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "synthetic history"}})
	if err != nil {
		t.Fatal(err)
	}
	return syncmsg.Response{ChatLogs: []bson.Raw{raw}}
}
func testAdmission(t *testing.T, path string) *historyAdmission {
	t.Helper()
	a, err := openHistoryAdmission(path, 1000, HistoryPacing{MinimumInterval: 20 * time.Millisecond, ErrorBackoff: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestHistoryAdmissionCoalescesConcurrentProductionRequests(t *testing.T) {
	a := testAdmission(t, "")
	req := syncmsg.Request{ChatID: 42, Cur: 100, Max: 103}
	want := admissionTestResponse(t, 103)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan syncmsg.Response, 2)
	var calls atomic.Int32
	fetch := func(context.Context) (syncmsg.Response, error) {
		calls.Add(1)
		close(entered)
		<-release
		return want, nil
	}
	go func() {
		r, err := a.run(t.Context(), req, fetch)
		if err != nil {
			t.Error(err)
		}
		done <- r
	}()
	<-entered
	// Join the real flight and cancel the joiner independently. It must not cancel
	// the owner or cause another dispatch when a later caller joins.
	ctx, cancel := context.WithCancel(t.Context())
	cancelled := make(chan error, 1)
	go func() { _, err := a.run(ctx, req, fetch); cancelled <- err }()
	waitHistoryDemands(t, a, 2)
	cancel()
	if err := <-cancelled; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	waitHistoryDemands(t, a, 1)
	joined := make(chan struct{})
	go func() {
		close(joined)
		r, err := a.run(t.Context(), req, fetch)
		if err != nil {
			t.Error(err)
		}
		done <- r
	}()
	<-joined
	waitHistoryDemands(t, a, 2)
	close(release)
	first, second := <-done, <-done
	if calls.Load() != 1 || len(first.ChatLogs) != 1 || len(second.ChatLogs) != 1 {
		t.Fatalf("calls=%d responses=%+v %+v", calls.Load(), first, second)
	}
	first.ChatLogs[0][0] ^= 1
	if err := second.ChatLogs[0].Validate(); err != nil {
		t.Fatal("coalesced callers shared mutable response bytes")
	}
}

func TestHistoryAdmissionDurableCooldownAndCancellation(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "admission.json")
	a := testAdmission(t, path)
	req := syncmsg.Request{ChatID: 42, Cur: 100, Max: 103}
	var calls atomic.Int32
	fetch := func(context.Context) (syncmsg.Response, error) {
		calls.Add(1)
		return admissionTestResponse(t, 103), nil
	}
	started := time.Now()
	if _, err := a.run(t.Context(), req, fetch); err != nil {
		t.Fatal(err)
	}
	state, found, err := privatejson.Read[historyAdmissionState](path)
	if err != nil || !found || !state.NextAllowed.After(started) {
		t.Fatalf("missing durable spacing: %+v %v", state, err)
	}
	// Reopen with a future reservation so a fresh process cannot dispatch before it.
	state.NextAllowed = time.Now().Add(time.Second)
	if err = privatejson.WriteAtomic(path, state); err != nil {
		t.Fatal(err)
	}
	reopened := testAdmission(t, path)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if _, err = reopened.run(ctx, req, fetch); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("cancelled/restarted demand reached source")
	}
	if _, err = openHistoryAdmission(path, 2000, HistoryPacing{}); !errors.Is(err, privatejson.ErrCorrupt) {
		t.Fatal("another account reused admission state", err)
	}
}

func TestHistoryAdmissionPersistsReservationBeforeFetchAndBacksOffNonProgress(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "admission.json")
	a := testAdmission(t, path)
	a.policy.ErrorBackoff = time.Hour
	req := syncmsg.Request{ChatID: 42, Cur: 100, Max: 103}
	if _, err := a.run(t.Context(), req, func(context.Context) (syncmsg.Response, error) {
		state, found, err := privatejson.Read[historyAdmissionState](path)
		if err != nil || !found || time.Until(state.NextAllowed) < 59*time.Minute {
			t.Error("request did not reserve crash cooldown before fetch")
		}
		return admissionTestResponse(t, 100), nil // inclusive boundary only
	}); !errors.Is(err, ErrGapUnresolved) {
		t.Fatal(err)
	}
	state, _, err := privatejson.Read[historyAdmissionState](path)
	if err != nil || time.Until(state.NextAllowed) < 59*time.Minute {
		t.Fatal("non-progress did not preserve error cooldown", err)
	}
}

func TestHistoryAdmissionWriteFailureFailsClosed(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	a := testAdmission(t, filepath.Join(dir, "admission.json"))
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	fetch := func(context.Context) (syncmsg.Response, error) {
		t.Fatal("dispatched without durable reservation")
		return syncmsg.Response{}, nil
	}
	req := syncmsg.Request{ChatID: 42, Cur: 100, Max: 103}
	if _, err := a.run(t.Context(), req, fetch); !errors.Is(err, privatejson.ErrUnsafePermissions) {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := a.run(t.Context(), req, fetch); !errors.Is(err, privatejson.ErrUnsafePermissions) {
		t.Fatal("failed admission did not remain closed", err)
	}
}

func waitHistoryDemands(t *testing.T, a *historyAdmission, n int) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		a.mu.Lock()
		got := a.pending
		a.mu.Unlock()
		if got == n {
			return
		}
		select {
		case <-timer.C:
			t.Fatalf("pending demand count=%d want=%d", got, n)
		case <-ticker.C:
		}
	}
}

func TestClientHistoryAdmissionUsesRealHistoryReader(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	check := checkSyncRequest(42, 100, 103)
	backend := newScriptedBackend(t, false, testloco.Expect("SYNCMSG", func(raw bson.Raw) error {
		if err := check(raw); err != nil {
			return err
		}
		close(entered)
		<-release
		return nil
	}, statusDocument(bson.E{Key: "chatLogs", Value: bson.A{
		bson.D{{Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "one"}},
		bson.D{{Key: "logId", Value: int64(103)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "three"}},
	}})))
	checkpoint := testCheckpoint(t)
	api := testContinuityClient(t, checkpoint, backend)
	api.historyAdmission = testAdmission(t, "")
	done := make(chan int, 2)
	for _, limit := range []int{1, 2} {
		go func() {
			page, err := api.ReadHistoryPage(t.Context(), 42, 100, 103, limit)
			if err != nil {
				t.Error(err)
			}
			done <- len(page.Events)
		}()
	}
	<-entered
	waitHistoryDemands(t, api.historyAdmission, 2)
	once.Do(func() { close(release) })
	first, second := <-done, <-done
	if first+second != 3 {
		t.Fatalf("coalesced request did not preserve separate delivery limits: %d %d", first, second)
	}
	if checkpoint.Snapshot().CommittedMax(42) != 0 || checkpoint.ReadWatermark(42) != 0 {
		t.Fatal("acquisition changed delivery/read progress")
	}
	waitBackends(t, backend)
}

func TestClientHistoryAdmissionShutdownCancelsAndJoinsWaitingDemand(t *testing.T) {
	a := testAdmission(t, "")
	a.state.NextAllowed = time.Now().Add(time.Hour)
	api := &Client{historyAdmission: a, dial: func(context.Context, authstate.State) (*session, error) {
		t.Error("shutdown waiting demand dialed")
		return nil, ErrClientClosed
	}}
	done := make(chan error, 1)
	go func() { _, err := api.ReadHistoryPage(t.Context(), 42, 100, 103, 3); done <- err }()
	waitHistoryDemands(t, a, 1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := api.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrClientClosed) {
		t.Fatal(err)
	}
}

func TestHistoryAdmissionLimitsPendingDemands(t *testing.T) {
	a := testAdmission(t, "")
	a.policy.MaxPending = 1
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	done := make(chan error, 1)
	req := syncmsg.Request{ChatID: 42, Cur: 100, Max: 103}
	go func() {
		_, err := a.run(t.Context(), req, func(context.Context) (syncmsg.Response, error) {
			close(entered)
			<-release
			return admissionTestResponse(t, 103), nil
		})
		done <- err
	}()
	<-entered
	if _, err := a.run(t.Context(), req, func(context.Context) (syncmsg.Response, error) {
		t.Error("overflow reached source")
		return syncmsg.Response{}, nil
	}); !errors.Is(err, ErrHistoryBusy) {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPacedCatchUpNonProgressRetainsDurableGap(t *testing.T) {
	checkpoint := testCheckpoint(t)
	backend := newScriptedBackend(t, false, testloco.Expect("SYNCMSG", checkSyncRequest(42, 0, 103), statusDocument()))
	api := testContinuityClient(t, checkpoint, backend)
	api.historyAdmission = testAdmission(t, "")
	if _, err := api.CatchUp(t.Context(), 42, 103); !errors.Is(err, ErrGapUnresolved) {
		t.Fatal(err)
	}
	state := checkpoint.Snapshot()
	if !state.HasGapThrough(42, 103) || state.CommittedMax(42) != 0 || checkpoint.ReadWatermark(42) != 0 {
		t.Fatal("paced non-progress lost the durable unresolved gap")
	}
	waitBackends(t, backend)
}

func TestHistoryAdmissionSerializesAndSpacesDifferentRooms(t *testing.T) {
	a := testAdmission(t, "")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	done := make(chan error, 2)
	var active atomic.Int32
	finished := make(chan time.Time, 1)
	secondStarted := make(chan time.Time, 1)
	firstReq := syncmsg.Request{ChatID: 42, Cur: 100, Max: 103}
	secondReq := syncmsg.Request{ChatID: 43, Cur: 100, Max: 103}
	go func() {
		_, err := a.run(t.Context(), firstReq, func(context.Context) (syncmsg.Response, error) {
			if active.Add(1) != 1 {
				t.Error("source acquisition overlapped")
			}
			close(entered)
			<-release
			active.Add(-1)
			finished <- time.Now()
			return admissionTestResponse(t, 103), nil
		})
		done <- err
	}()
	<-entered
	go func() {
		_, err := a.run(t.Context(), secondReq, func(context.Context) (syncmsg.Response, error) {
			if active.Add(1) != 1 {
				t.Error("source acquisition overlapped")
			}
			secondStarted <- time.Now()
			active.Add(-1)
			return admissionTestResponse(t, 103), nil
		})
		done <- err
	}()
	waitHistoryDemands(t, a, 2)
	once.Do(func() { close(release) })
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if gap := (<-secondStarted).Sub(<-finished); gap < a.policy.MinimumInterval {
		t.Fatalf("source requests spaced only %s", gap)
	}
}
