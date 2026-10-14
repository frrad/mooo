package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/privatejson"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
)

func TestUnavailableHistoryRestartScopeExpiryAndPositivePrecedence(t *testing.T) {
	s, dir := testHistoryStore(t)
	req := syncmsg.Request{ChatID: 42, Cur: 100, Max: 300}
	if err := s.save(req, syncmsg.Response{MinLogID: 350}); !errors.Is(err, ErrHistoryBelowFloor) {
		t.Fatal(err)
	}
	reopened := reopenTestHistoryStore(t, dir)
	if _, found, err := reopened.lookup(req); !found || !errors.Is(err, ErrHistoryBelowFloor) {
		t.Fatal(found, err)
	}
	for _, other := range []syncmsg.Request{{ChatID: 43, Cur: 100, Max: 300}, {ChatID: 42, Cur: 101, Max: 300}, {ChatID: 42, Cur: 100, Max: 299}, {ChatID: 42, Cur: 100, Max: 300, Count: 1}} {
		if _, found, err := reopened.lookup(other); err != nil || found {
			t.Fatal("inferred unavailable scope", other, found, err)
		}
	}
	page, err := reopened.window(t.Context(), StoredHistoryRequest{ChatID: 42, After: 100, Through: 300, Limit: 3})
	if err != nil || page.Unavailable == nil || page.Complete || page.HasMore || len(page.Events) != 0 {
		t.Fatal("unavailable became acquired coverage", page, err)
	}
	// Fresh positive coverage wins over an earlier empty observation.
	if err = s.save(req, admissionTestResponse(t, 300)); err != nil {
		t.Fatal(err)
	}
	positive := reopenTestHistoryStore(t, dir)
	page, err = positive.window(t.Context(), StoredHistoryRequest{ChatID: 42, After: 100, Through: 300, Limit: 3})
	if err != nil || !page.Complete || page.Unavailable != nil || len(page.Events) != 1 {
		t.Fatal(page, err)
	}

	expired, expiredDir := testHistoryStore(t)
	if err = expired.save(req, syncmsg.Response{MinLogID: 350}); !errors.Is(err, ErrHistoryBelowFloor) {
		t.Fatal(err)
	}
	r := expired.unavailable[req]
	r.Observation.ObservedAt = time.Now().Add(-2 * historyUnavailableFreshness)
	r.Observation.RetryAfter = r.Observation.ObservedAt.Add(historyUnavailableFreshness)
	if err = privatejson.WriteAtomic(filepath.Join(expiredDir, unavailableName(req)), r); err != nil {
		t.Fatal(err)
	}
	if _, found, err := expired.lookup(req); err != nil || found {
		t.Fatal("expired observation blocks acquisition", found, err)
	}
	after := reopenTestHistoryStore(t, expiredDir)
	if _, found, err := after.lookup(req); err != nil || found {
		t.Fatal(found, err)
	}
	if _, err = os.Stat(filepath.Join(expiredDir, unavailableName(req))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("expired record retained", err)
	}
}

func TestUnavailableHistoryCorruptionAndUnexplainedEmptyFailClosed(t *testing.T) {
	req := syncmsg.Request{ChatID: 42, Max: 300}
	for _, floor := range []int64{0, 300} {
		s, _ := testHistoryStore(t)
		if err := s.save(req, syncmsg.Response{MinLogID: floor}); !errors.Is(err, ErrGapUnresolved) {
			t.Fatal(err)
		}
		if len(s.unavailable) != 0 {
			t.Fatal("unexplained empty result cached")
		}
	}
	s, dir := testHistoryStore(t)
	if err := s.save(req, syncmsg.Response{MinLogID: 350}); !errors.Is(err, ErrHistoryBelowFloor) {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, unavailableName(req)), []byte("not-json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.lookup(req); !errors.Is(err, ErrHistoryStoreCorrupt) {
		t.Fatal("corrupt snapshot became miss", err)
	}
	if _, err := openHistoryStore(dir, reusableTestState().Credentials.UserID, reusableTestState().Identity.DeviceUUID); !errors.Is(err, ErrHistoryStoreCorrupt) {
		t.Fatal(err)
	}
}

func TestUnavailableHistoryClosedClientNeverServesObservation(t *testing.T) {
	c := accountProfile(t, accountRoot(t), "negative", 1000, HistoryPacing{})
	req := syncmsg.Request{ChatID: 42, Max: 300}
	if err := c.historyStore.save(req, syncmsg.Response{MinLogID: 350}); !errors.Is(err, ErrHistoryBelowFloor) {
		t.Fatal(err)
	}
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadHistoryPage(t.Context(), 42, 0, 300, 1); !errors.Is(err, ErrClientClosed) {
		t.Fatal(err)
	}
}

func TestUnavailableHistoryOpenedProfileRestartNeverDials(t *testing.T) {
	root := accountRoot(t)
	c := accountProfile(t, root, "negative-restart", 1000, HistoryPacing{})
	req := syncmsg.Request{ChatID: 42, Max: 300}
	if err := c.historyStore.save(req, syncmsg.Response{MinLogID: 350}); !errors.Is(err, ErrHistoryBelowFloor) {
		t.Fatal(err)
	}
	deny := func(context.Context, authstate.State) (*session, error) {
		t.Error("unavailable snapshot reopened source")
		return nil, ErrProtocol
	}
	c.dial = deny
	var first *HistoryUnavailableError
	if _, err := c.ReadHistoryPage(t.Context(), 42, 0, 300, 1); !errors.As(err, &first) {
		t.Fatal(err)
	}
	if err := c.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithOptions(filepath.Join(root, "negative-restart"), nil, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	reopened.dial = deny
	var replay *HistoryUnavailableError
	if _, err = reopened.ReadHistoryPage(t.Context(), 42, 0, 300, 1); !errors.As(err, &replay) || !replay.Observation.RetryAfter.Equal(first.Observation.RetryAfter) {
		t.Fatal("restart refreshed or lost observation", err)
	}
	window, err := reopened.ReadStoredHistory(t.Context(), StoredHistoryRequest{ChatID: 42, Through: 300, Limit: 1})
	if err != nil || window.Complete || window.Unavailable == nil {
		t.Fatal(window, err)
	}
	// Expiry permits one fresh demand through real admission/session opening.
	r := reopened.historyStore.unavailable[req]
	r.Observation.ObservedAt = time.Now().Add(-2 * historyUnavailableFreshness)
	r.Observation.RetryAfter = r.Observation.ObservedAt.Add(historyUnavailableFreshness)
	if err = privatejson.WriteAtomic(filepath.Join(reopened.historyStore.dir, unavailableName(req)), r); err != nil {
		t.Fatal(err)
	}
	dials := 0
	reopened.dial = func(context.Context, authstate.State) (*session, error) {
		dials++
		return nil, ErrProtocol
	}
	if _, err = reopened.ReadHistoryPage(t.Context(), 42, 0, 300, 1); !errors.Is(err, ErrProtocol) || dials != 1 || reopened.historyAdmission.state.LastErrorAt.IsZero() {
		t.Fatal("expired observation bypassed fresh admission", dials, err)
	}
}

func TestUnavailableHistorySnapshotCapacityAndTimestampGuards(t *testing.T) {
	s, dir := testHistoryStore(t)
	now := time.Now()
	s.unavailable = make(map[syncmsg.Request]historyUnavailableRecord)
	for i := 1; i <= historyUnavailableMaximum; i++ {
		req := syncmsg.Request{ChatID: int64(i), Max: 300}
		s.unavailable[req] = historyUnavailableRecord{Version: 1, Binding: s.binding, Request: req, Observation: HistoryUnavailable{MinLogID: 350, ObservedAt: now, RetryAfter: now.Add(historyUnavailableFreshness)}}
	}
	req := syncmsg.Request{ChatID: historyUnavailableMaximum + 1, Max: 300}
	if err := s.save(req, syncmsg.Response{MinLogID: 350}); !errors.Is(err, ErrHistoryStoreCapacity) {
		t.Fatal("unbounded unavailable snapshots", err)
	}
	if _, err := os.Stat(filepath.Join(dir, unavailableName(req))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("capacity overflow wrote a snapshot", err)
	}
	future, futureDir := testHistoryStore(t)
	if err := future.save(req, syncmsg.Response{MinLogID: 350}); !errors.Is(err, ErrHistoryBelowFloor) {
		t.Fatal(err)
	}
	r := future.unavailable[req]
	r.Observation.ObservedAt = time.Now().Add(time.Hour)
	r.Observation.RetryAfter = r.Observation.ObservedAt.Add(historyUnavailableFreshness)
	if err := privatejson.WriteAtomic(filepath.Join(futureDir, unavailableName(req)), r); err != nil {
		t.Fatal(err)
	}
	if _, err := openHistoryStore(futureDir, reusableTestState().Credentials.UserID, reusableTestState().Identity.DeviceUUID); !errors.Is(err, ErrHistoryStoreCorrupt) {
		t.Fatal("future observation accepted", err)
	}
}
