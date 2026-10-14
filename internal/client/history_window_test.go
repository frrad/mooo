package client

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func windowClient(t *testing.T) (*Client, *historyStore) {
	t.Helper()
	store, _ := testHistoryStore(t)
	return &Client{historyStore: store}, store
}
func windowIDs(t *testing.T, page StoredHistoryPage) []int64 {
	t.Helper()
	ids := []int64{}
	for _, evt := range page.Events {
		_, id, ok := events.MessagePosition(evt)
		if !ok {
			t.Fatal("missing identity")
		}
		ids = append(ids, id)
	}
	return ids
}
func TestStoredWindowSparseBackwardAnchorsAndRestart(t *testing.T) {
	api, store := windowClient(t)
	req := syncmsg.Request{ChatID: 42, Cur: 0, Max: 900}
	logs := []bson.Raw{historyStoreRaw(t, 10, "old"), historyStoreRaw(t, 200, "middle"), historyStoreRaw(t, 900, "new")}
	if err := store.save(req, syncmsg.Response{ChatLogs: logs}); err != nil {
		t.Fatal(err)
	}
	first, err := api.ReadStoredHistory(t.Context(), StoredHistoryRequest{ChatID: 42, Through: 900, Limit: 2})
	if err != nil || !first.Complete || !first.HasMore || !reflect.DeepEqual(windowIDs(t, first), []int64{200, 900}) {
		t.Fatal(first, err)
	}
	api.historyStore = reopenTestHistoryStore(t, store.dir)
	older, err := api.ReadStoredHistory(t.Context(), StoredHistoryRequest{ChatID: 42, Through: 200, ExcludeThrough: true, Limit: 2})
	if err != nil || !older.Complete || older.HasMore || !reflect.DeepEqual(windowIDs(t, older), []int64{10}) {
		t.Fatal(older, err)
	}
	forward, err := api.ReadStoredHistory(t.Context(), StoredHistoryRequest{ChatID: 42, After: 200, Through: 900, Limit: 2})
	if err != nil || !forward.Complete || !reflect.DeepEqual(windowIDs(t, forward), []int64{900}) {
		t.Fatal(forward, err)
	}
}
func TestStoredWindowReportsMissingPrefixAndAppliesTargets(t *testing.T) {
	api, store := windowClient(t)
	if err := store.save(syncmsg.Request{ChatID: 42, Cur: 100, Max: 103}, syncmsg.Response{ChatLogs: []bson.Raw{historyStoreRaw(t, 101, "secret deleted text"), historyStoreRaw(t, 103, "later")}}); err != nil {
		t.Fatal(err)
	}
	if err := store.observeCurrentRecords(42, []bson.Raw{mustBSON(bson.D{{Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(16385)}, {Key: "message", Value: "secret deleted text"}})}); err != nil {
		t.Fatal(err)
	}
	page, err := api.ReadStoredHistory(t.Context(), StoredHistoryRequest{ChatID: 42, Through: 103, Limit: 2})
	if err != nil || page.Complete || len(page.Events) != 2 {
		t.Fatal(page, err)
	}
	if _, ok := page.Events[0].(events.DeletedMessage); !ok {
		t.Fatal("rendered retained deleted text")
	}
	if err := store.observe(events.MessageEdited{ChatID: 42, LogID: 104, TargetLogID: 103, TargetRevision: 2}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = api.ReadStoredHistory(t.Context(), StoredHistoryRequest{ChatID: 42, Through: 103, Limit: 2}); !errors.Is(err, ErrHistoryTargetUnresolved) {
		t.Fatal(err)
	}
}
func TestStoredWindowEmptyIsNotAcquiredCompletion(t *testing.T) {
	api, _ := windowClient(t)
	page, err := api.ReadStoredHistory(t.Context(), StoredHistoryRequest{ChatID: 42, Through: 100, Limit: 50})
	if err != nil || page.Complete || len(page.Events) != 0 {
		t.Fatal(page, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = api.ReadStoredHistory(ctx, StoredHistoryRequest{ChatID: 42, Through: 100, Limit: 50}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestStoredWindowCoverageStopsBeforeDisconnectedIsland(t *testing.T) {
	api, store := windowClient(t)
	for _, page := range []struct {
		after, through int64
		ids            []int64
	}{
		{0, 900, []int64{100, 500}},
		{700, 900, []int64{800, 900}},
	} {
		var logs []bson.Raw
		for _, id := range page.ids {
			logs = append(logs, historyStoreRaw(t, id, "synthetic"))
		}
		if err := store.save(syncmsg.Request{ChatID: 42, Cur: page.after, Max: page.through}, syncmsg.Response{ChatLogs: logs}); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		for _, selection := range []struct {
			after, through, covered int64
			complete                bool
		}{
			{0, 900, 500, false}, {500, 900, 500, false}, {700, 900, 900, true}, {0, 300, 300, true},
		} {
			p, err := api.ReadStoredHistory(t.Context(), StoredHistoryRequest{ChatID: 42, After: selection.after, Through: selection.through, Limit: 1})
			if err != nil || p.CoveredThrough != selection.covered || p.Complete != selection.complete {
				t.Fatalf("coverage=%d complete=%v err=%v", p.CoveredThrough, p.Complete, err)
			}
		}
		api.historyStore = reopenTestHistoryStore(t, store.dir)
	}
}

func TestStoredWindowDoesNotCrossGapBeforeCachedSuffix(t *testing.T) {
	api, store := windowClient(t)
	for _, p := range []struct {
		after, through int64
		ids            []int64
	}{
		{0, 500, []int64{100, 500}}, {700, 900, []int64{800, 900}},
	} {
		var logs []bson.Raw
		for _, n := range p.ids {
			logs = append(logs, historyStoreRaw(t, n, "synthetic"))
		}
		if err := store.save(syncmsg.Request{ChatID: 42, Cur: p.after, Max: p.through}, syncmsg.Response{ChatLogs: logs}); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		p, err := api.ReadStoredHistory(t.Context(), StoredHistoryRequest{ChatID: 42, Through: 900, Limit: 50})
		if err != nil || !reflect.DeepEqual(windowIDs(t, p), []int64{800, 900}) || !p.HasMore || !p.CachedSuffix || p.Complete {
			t.Fatalf("crossed gap or retired missing prefix: ids=%v more=%v complete=%v err=%v", windowIDs(t, p), p.HasMore, p.Complete, err)
		}
		api.historyStore = reopenTestHistoryStore(t, store.dir)
	}
}

func TestStoredWindowMissingRangeStopsAtNextAcquiredPage(t *testing.T) {
	api, store := windowClient(t)
	if err := store.save(syncmsg.Request{ChatID: 42, Cur: 300, Max: 500}, syncmsg.Response{ChatLogs: []bson.Raw{historyStoreRaw(t, 400, "cached"), historyStoreRaw(t, 500, "cached")}}); err != nil {
		t.Fatal(err)
	}
	p, err := api.ReadStoredHistory(t.Context(), StoredHistoryRequest{ChatID: 42, Through: 400, ExcludeThrough: true, Limit: 50})
	if err != nil || p.CoveredThrough != 0 || p.MissingThrough != 300 || p.Complete || len(p.Events) != 0 {
		t.Fatal(p, err)
	}
	// The bridge must acquire only (0,300], then re-read the joined local window.
	if err := store.save(syncmsg.Request{ChatID: 42, Cur: 0, Max: 300}, syncmsg.Response{ChatLogs: []bson.Raw{historyStoreRaw(t, 100, "older"), historyStoreRaw(t, 300, "join")}}); err != nil {
		t.Fatal(err)
	}
	p, err = api.ReadStoredHistory(t.Context(), StoredHistoryRequest{ChatID: 42, Through: 400, ExcludeThrough: true, Limit: 50})
	if err != nil || !p.Complete || !reflect.DeepEqual(windowIDs(t, p), []int64{100, 300}) {
		t.Fatal(p, err)
	}
}
