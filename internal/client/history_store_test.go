package client

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/privatejson"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func testHistoryStore(t *testing.T) (*historyStore, string) {
	t.Helper()
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "acquired")
	state := reusableTestState()
	store, err := openHistoryStore(dir, state.Credentials.UserID, state.Identity.DeviceUUID)
	if err != nil {
		t.Fatal(err)
	}
	return store, dir
}
func reopenTestHistoryStore(t *testing.T, dir string) *historyStore {
	t.Helper()
	state := reusableTestState()
	s, err := openHistoryStore(dir, state.Credentials.UserID, state.Identity.DeviceUUID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func historyStoreRaw(t *testing.T, id int64, text string) bson.Raw {
	t.Helper()
	raw, err := bson.Marshal(bson.D{{Key: "logId", Value: id}, {Key: "type", Value: int32(1)}, {Key: "message", Value: text}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestHistoryStoreProductionReaderRetainsFullPageAcrossRestart(t *testing.T) {
	store, dir := testHistoryStore(t)
	checkpoint := testCheckpoint(t)
	backend := newScriptedBackend(t, false, testloco.Expect("SYNCMSG", checkSyncRequest(42, 100, 103), statusDocument(bson.E{Key: "chatLogs", Value: bson.A{
		historyStoreRaw(t, 101, "first"), historyStoreRaw(t, 103, "tail"),
	}})))
	api := testContinuityClient(t, checkpoint, backend)
	api.historyStore = store
	page, err := api.ReadHistoryPage(t.Context(), 42, 100, 103, 1)
	if err != nil || page.Next != 101 || len(page.Events) != 1 {
		t.Fatalf("first delivery: %+v %v", page, err)
	}
	waitBackends(t, backend)
	if err := api.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	restarted, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	restarted.historyStore = reopenTestHistoryStore(t, dir)
	restarted.checkpoint = checkpoint
	restarted.dial = func(context.Context, authstate.State) (*session, error) {
		t.Error("stored tail refetched source")
		return nil, ErrProtocol
	}
	page, err = restarted.ReadHistoryPage(t.Context(), 42, 101, 103, 1)
	if err != nil || !page.Complete || page.Next != 103 || page.Events[0].(events.TextMessage).Message != "tail" {
		t.Fatalf("stored tail: %+v %v", page, err)
	}
	if checkpoint.Snapshot().CommittedMax(42) != 0 || checkpoint.ReadWatermark(42) != 0 {
		t.Fatal("acquired page became delivery/read progress")
	}
	// Repeated reads remain local, and cancellation/closure still reject admission.
	if _, err = restarted.ReadHistoryPage(t.Context(), 42, 100, 103, 2); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = restarted.ReadHistoryPage(ctx, 42, 100, 103, 2); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = restarted.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.ReadHistoryPage(t.Context(), 42, 100, 103, 2); !errors.Is(err, ErrClientClosed) {
		t.Fatal(err)
	}
}

type historyEditFixture struct {
	Provenance struct {
		Kind string `json:"kind"`
	}
	Push struct {
		Edit struct {
			Method string
			Body   json.RawMessage
		}
		Delete struct {
			Method string
			Body   json.RawMessage
		}
	}
	CatchUp []json.RawMessage
}

func loadHistoryEditFixture(t *testing.T) historyEditFixture {
	t.Helper()
	data, err := os.ReadFile("../../research/fixtures/edits/observed-group-edit-delete.json")
	if err != nil {
		t.Fatal(err)
	}
	var f historyEditFixture
	if err = json.Unmarshal(data, &f); err != nil || f.Provenance.Kind != "observed" {
		t.Fatal("invalid observed edit fixture", err)
	}
	return f
}
func historyFixturePacket(t *testing.T, method string, body json.RawMessage) loco.Packet {
	t.Helper()
	var doc bson.D
	if err := bson.UnmarshalExtJSON(body, false, &doc); err != nil {
		t.Fatal(err)
	}
	raw, err := bson.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return loco.Packet{Header: loco.Header{Method: method}, Body: raw}
}

func TestHistoryStoreObservedLiveEditsAndDeletesSurviveRestart(t *testing.T) {
	store, dir := testHistoryStore(t)
	f := loadHistoryEditFixture(t)
	const first int64 = 3000000000000000201
	const deleted int64 = 3000000000000000203
	req := syncmsg.Request{ChatID: 3000, Cur: first - 1, Max: deleted}
	if err := store.save(req, syncmsg.Response{ChatLogs: []bson.Raw{historyStoreRaw(t, first, "synthetic original"), historyStoreRaw(t, deleted, "synthetic deleted text that must not be shown")}}); err != nil {
		t.Fatal(err)
	}
	api := &Client{historyStore: store}
	input := make(chan loco.Packet, 2)
	input <- historyFixturePacket(t, f.Push.Edit.Method, f.Push.Edit.Body)
	input <- historyFixturePacket(t, f.Push.Delete.Method, f.Push.Delete.Body)
	close(input)
	output := make(chan events.Result, 2)
	// Execute the actual production decoder observer, including mutations before
	// delivery/committed filtering, rather than calling a replica of the cache rules.
	decodeEventStreamWithTerminalStop(input, output, nil, nil, nil, nil, nil, api.observeHistoryPacket)
	for result := range output {
		if result.Err != nil {
			t.Fatal(result.Err)
		}
	}
	store = reopenTestHistoryStore(t, dir)
	response, found, err := store.lookup(req)
	if err != nil || !found {
		t.Fatal(err)
	}
	page, err := decodeHistoryPage(req, response, 3, nil)
	if err != nil || len(page.Events) != 2 {
		t.Fatalf("projection: %+v %v", page, err)
	}
	if page.Events[0].(events.TextMessage).Message != "synthetic original-edited" {
		t.Fatal("old cached edit body survived")
	}
	if _, ok := page.Events[1].(events.DeletedMessage); !ok {
		t.Fatalf("deleted text exposed as %T", page.Events[1])
	}
}

func TestHistoryStoreUnresolvedEditCannotBeSkippedByANewerPage(t *testing.T) {
	store, _ := testHistoryStore(t)
	req := syncmsg.Request{ChatID: 42, Cur: 100, Max: 110}
	if err := store.save(req, syncmsg.Response{ChatLogs: []bson.Raw{historyStoreRaw(t, 101, "before"), historyStoreRaw(t, 103, "old target"), historyStoreRaw(t, 110, "after")}}); err != nil {
		t.Fatal(err)
	}
	// An edit feed without its target cannot authorize returning the old text.
	if err := store.observeCurrentRecords(42, []bson.Raw{func() bson.Raw {
		raw, err := bson.Marshal(bson.D{{Key: "logId", Value: int64(111)}, {Key: "type", Value: int32(0)}, {Key: "message", Value: `{"feedType":25,"logId":103,"targetRevision":1}`}})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}()}); err != nil {
		t.Fatal(err)
	}
	response, found, err := store.lookup(req)
	if err != nil || !found || len(response.ChatLogs) != 1 {
		t.Fatal("safe prefix was discarded", err)
	}
	// A later fetch that omits the known unresolved target must not leap over it.
	after := syncmsg.Request{ChatID: 42, Cur: 101, Max: 110}
	if err = store.save(after, syncmsg.Response{ChatLogs: []bson.Raw{historyStoreRaw(t, 110, "after")}}); err != nil {
		t.Fatal(err)
	}
	if _, found, err = store.lookup(after); err != nil || found {
		t.Fatal("unresolved acquired target was skipped", err)
	}
	revised, err := bson.Marshal(bson.D{{Key: "logId", Value: int64(103)}, {Key: "type", Value: int32(1)}, {Key: "revision", Value: int32(1)}, {Key: "message", Value: "new target"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.observeCurrentRecords(42, []bson.Raw{revised}); err != nil {
		t.Fatal(err)
	}
	// Select the older complete snapshot to retain the formerly unavailable target.
	// Cache choice must not discard that target merely because the newer page has a larger Cur.
	response, found, err = store.lookup(after)
	if err != nil || !found {
		t.Fatal(err)
	}
	page, err := decodeHistoryPage(after, response, 3, nil)
	if err != nil || len(page.Events) != 2 || page.Events[0].(events.TextMessage).Message != "new target" {
		t.Fatalf("reconciled target lost: %+v %v", page, err)
	}
}

func TestHistoryStoreBindingAndCorruptionFailClosed(t *testing.T) {
	store, dir := testHistoryStore(t)
	req := syncmsg.Request{ChatID: 42, Cur: 100, Max: 103}
	if err := store.save(req, syncmsg.Response{ChatLogs: []bson.Raw{historyStoreRaw(t, 103, "private synthetic")}}); err != nil {
		t.Fatal(err)
	}
	state := reusableTestState()
	if _, err := openHistoryStore(dir, state.Credentials.UserID+1, state.Identity.DeviceUUID); !errors.Is(err, ErrHistoryStoreCorrupt) {
		t.Fatal(err)
	}
	if _, err := openHistoryStore(dir, state.Credentials.UserID, "another-owned-device"); !errors.Is(err, ErrHistoryStoreCorrupt) {
		t.Fatal(err)
	}
	path := filepath.Join(dir, historyPageName(req))
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.lookup(req); !errors.Is(err, ErrHistoryStoreCorrupt) {
		t.Fatal("corrupt page became a miss/refetch", err)
	}
	if _, err := openHistoryStore(dir, state.Credentials.UserID, state.Identity.DeviceUUID); !errors.Is(err, ErrHistoryStoreCorrupt) {
		t.Fatal(err)
	}
	if err := privatejson.ValidatePrivateFile(filepath.Join(dir, "header.json")); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryStoreCachedPrefixesRemainUsefulWhenCeilingChanges(t *testing.T) {
	store, _ := testHistoryStore(t)
	if err := store.save(syncmsg.Request{ChatID: 42, Cur: 100, Max: 103}, syncmsg.Response{ChatLogs: []bson.Raw{historyStoreRaw(t, 101, "one"), historyStoreRaw(t, 103, "three")}}); err != nil {
		t.Fatal(err)
	}
	request := syncmsg.Request{ChatID: 42, Cur: 100, Max: 110}
	response, found, err := store.lookup(request)
	if err != nil || !found || len(response.ChatLogs) != 2 {
		t.Fatal("known prefix refetched when ceiling advanced", err)
	}
	page, err := decodeHistoryPage(request, response, 3, nil)
	if err != nil || page.Next != 103 || page.Complete {
		t.Fatalf("prefix became completion: %+v %v", page, err)
	}
	// A narrower selection must never inherit records outside its requested bounds.
	response, found, err = store.lookup(syncmsg.Request{ChatID: 42, Cur: 100, Max: 101})
	if err != nil || !found || len(response.ChatLogs) != 1 {
		t.Fatal("cached selection broadened", err)
	}
}

func TestHistoryStoreCatchUpProjectsBufferedDeletionBeforeDelivery(t *testing.T) {
	store, _ := testHistoryStore(t)
	if err := store.save(syncmsg.Request{ChatID: 42, Cur: 100, Max: 110}, syncmsg.Response{ChatLogs: []bson.Raw{historyStoreRaw(t, 101, "must never be replayed after known deletion")}}); err != nil {
		t.Fatal(err)
	}
	checkpoint := testCheckpoint(t)
	if _, err := checkpoint.CommitMessage(42, 100); err != nil {
		t.Fatal(err)
	}
	backend := newScriptedBackend(t, false, testloco.Expect("SYNCMSG", checkSyncRequest(42, 101, 110), statusDocument(bson.E{Key: "chatLogs", Value: bson.A{
		bson.D{{Key: "logId", Value: int64(110)}, {Key: "type", Value: int32(0)}, {Key: "message", Value: `{"feedType":14,"logId":101}`}},
	}})))
	api := testContinuityClient(t, checkpoint, backend)
	api.historyStore = store
	got, err := api.CatchUp(t.Context(), 42, 110)
	if err != nil || len(got) != 2 {
		t.Fatalf("catch-up: %+v %v", got, err)
	}
	if _, ok := got[0].(events.DeletedMessage); !ok {
		t.Fatalf("buffered deleted content exposed as %T", got[0])
	}
	if _, ok := got[1].(events.MessageDeleted); !ok {
		t.Fatalf("deletion feed lost: %T", got[1])
	}
	if checkpoint.Snapshot().CommittedMax(42) != 100 {
		t.Fatal("acquisition committed delivery")
	}
	waitBackends(t, backend)
}

func TestHistoryStoreFailedPageWriteRetainsTargetSafetyAcrossRestart(t *testing.T) {
	store, dir := testHistoryStore(t)
	req := syncmsg.Request{ChatID: 42, Cur: 100, Max: 103}
	if err := store.save(req, syncmsg.Response{ChatLogs: []bson.Raw{historyStoreRaw(t, 103, "old target")}}); err != nil {
		t.Fatal(err)
	}
	// Persist a known deletion, then fail a later page write. Reopen must apply
	// the already durable target overlay rather than resurrect its old page body.
	if err := store.observeCurrentRecords(42, []bson.Raw{func() bson.Raw {
		raw, err := bson.Marshal(bson.D{{Key: "logId", Value: int64(104)}, {Key: "type", Value: int32(0)}, {Key: "message", Value: `{"feedType":14,"logId":103}`}})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}()}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := store.save(req, syncmsg.Response{ChatLogs: []bson.Raw{historyStoreRaw(t, 103, "late old response")}}); err == nil {
		t.Fatal("unsafe write succeeded")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	store = reopenTestHistoryStore(t, dir)
	response, found, err := store.lookup(req)
	if err != nil || !found {
		t.Fatal(err)
	}
	page, err := decodeHistoryPage(req, response, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := page.Events[0].(events.DeletedMessage); !ok {
		t.Fatal("failed page update resurrected old content")
	}
}

func TestHistoryStoreGetMessagesReconcilesCurrentTargetThroughProductionAPI(t *testing.T) {
	store, _ := testHistoryStore(t)
	req := syncmsg.Request{ChatID: 42, Cur: 100, Max: 103}
	if err := store.save(req, syncmsg.Response{ChatLogs: []bson.Raw{historyStoreRaw(t, 103, "old")}}); err != nil {
		t.Fatal(err)
	}
	feed, err := bson.Marshal(bson.D{{Key: "logId", Value: int64(110)}, {Key: "type", Value: int32(0)}, {Key: "message", Value: `{"feedType":25,"logId":103,"targetRevision":1}`}})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.observeCurrentRecords(42, []bson.Raw{feed}); err != nil {
		t.Fatal(err)
	}
	revised := bson.D{{Key: "logId", Value: int64(103)}, {Key: "type", Value: int32(1)}, {Key: "revision", Value: int32(1)}, {Key: "message", Value: "current"}}
	backend := newScriptedBackend(t, false, testloco.Expect("GETMSGS", nil, statusDocument(bson.E{Key: "chatLogs", Value: bson.A{revised}})))
	checkpoint := testCheckpoint(t)
	api := testContinuityClient(t, checkpoint, backend)
	api.historyStore = store
	if _, err = api.GetMessages(t.Context(), 42, []int64{103}); err != nil {
		t.Fatal(err)
	}
	page, err := api.ReadHistoryPage(t.Context(), 42, 100, 103, 1)
	if err != nil || page.Events[0].(events.TextMessage).Message != "current" {
		t.Fatalf("lookup did not reconcile cache: %+v %v", page, err)
	}
	if checkpoint.Snapshot().CommittedMax(42) != 0 {
		t.Fatal("target lookup advanced delivery")
	}
	waitBackends(t, backend)
}
