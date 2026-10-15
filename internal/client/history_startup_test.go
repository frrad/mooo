package client

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func bulkRoom(t *testing.T, id, last int64) bson.Raw {
	t.Helper()
	return mustBSON(bson.D{{Key: "c", Value: id}, {Key: "t", Value: "MultiChat"}, {Key: "ll", Value: last}})
}
func bulkLog(chat, id int64) bson.D {
	return bson.D{{Key: "chatId", Value: chat}, {Key: "logId", Value: id}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "synthetic acquired"}}
}
func checkBulk(ids, sinces []int64) func(bson.Raw) error {
	return func(raw bson.Raw) error {
		if err := requireExactKeys(raw, "chatIds", "sinces"); err != nil {
			return err
		}
		for key, want := range map[string][]int64{"chatIds": ids, "sinces": sinces} {
			vals, err := raw.Lookup(key).Array().Values()
			if err != nil || len(vals) != len(want) {
				return errors.New("bulk pair lengths")
			}
			for i, v := range vals {
				if v.Type != bson.TypeInt64 || v.Int64() != want[i] {
					return fmt.Errorf("bulk %s position %d differs", key, i)
				}
			}
		}
		return nil
	}
}

// startupOnly runs the connect chain with startup history and no rooms to
// catch up.
func startupOnly(t *testing.T, api *Client, options StartupHistoryOptions) (CatchUpResult, error) {
	t.Helper()
	return api.CatchUpAll(t.Context(), CatchUpOptions{StartupHistory: &options})
}
func startupClient(t *testing.T, backend *testloco.Backend, rooms []bson.Raw, floor int64) (*Client, string) {
	t.Helper()
	api := testContinuityClient(t, testCheckpoint(t), backend)
	api.historyStore, _ = testHistoryStore(t)
	dir := api.historyStore.dir
	api.historyAdmission = testAdmission(t, "")
	if err := api.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	s := fullChatListSession(t, rooms)
	api.session.loginCursor = s.loginCursor
	api.session.loginMinLogID = floor
	return api, dir
}
func TestStartupBulkPartialRetirementPersistsBeforeNextRequest(t *testing.T) {
	backend := newScriptedBackend(t, false,
		testloco.Expect("MCHATLOGS", checkBulk([]int64{42, 43}, []int64{0, 0}), statusDocument(bson.E{Key: "eof", Value: false}, bson.E{Key: "chatLogs", Value: bson.A{bulkLog(42, 101), bulkLog(43, 201)}})),
		testloco.Expect("MCHATLOGS", checkBulk([]int64{43}, []int64{201}), statusDocument(bson.E{Key: "eof", Value: true}, bson.E{Key: "chatLogs", Value: bson.A{bulkLog(43, 203)}})),
	)
	api, dir := startupClient(t, backend, []bson.Raw{bulkRoom(t, 43, 203), bulkRoom(t, 42, 101), bulkRoom(t, 44, 50)}, 100)
	if _, err := api.checkpoint.CommitMessage(42, 500); err != nil {
		t.Fatal(err)
	}
	if _, err := api.checkpoint.CommitReadWatermark(42, 400); err != nil {
		t.Fatal(err)
	}
	got, err := startupOnly(t, api, StartupHistoryOptions{})
	if err != nil || got.Pages != 2 || got.Logs != 3 || got.HistoryPending != 0 {
		t.Fatalf("bulk result %+v %v", got, err)
	}
	reopened := reopenTestHistoryStore(t, dir)
	if !reflect.DeepEqual(reopened.bulkSnapshot(), map[int64]int64{42: 101, 43: 203}) {
		t.Fatal("persisted bulk cursor differs")
	}
	if api.checkpoint.Snapshot().CommittedMax(42) != 500 || api.checkpoint.ReadWatermark(42) != 400 {
		t.Fatal("bulk changed live/read positions")
	}
	api.historyStore = reopened
	page, err := api.ReadHistoryPage(t.Context(), 43, 0, 203, 3)
	if err != nil || len(page.Events) != 2 || page.Next != 203 || page.Events[0].(events.TextMessage).LogID != 201 {
		t.Fatalf("bulk data unavailable through real reader: %+v %v", page, err)
	}
	waitBackends(t, backend)
}
func TestStartupBulkBatchesOneHundredValidRooms(t *testing.T) {
	ids := make([]int64, 100)
	zeros := make([]int64, 100)
	rooms := make([]bson.Raw, 0, 101)
	for i := range 101 {
		id := int64(i + 1)
		rooms = append(rooms, bulkRoom(t, id, 10))
		if i < 100 {
			ids[i] = id
		}
	}
	backend := newScriptedBackend(t, false,
		testloco.Expect("MCHATLOGS", checkBulk(ids, zeros), statusDocument(bson.E{Key: "eof", Value: true})),
		testloco.Expect("MCHATLOGS", checkBulk([]int64{101}, []int64{0}), statusDocument(bson.E{Key: "eof", Value: true})),
	)
	api, _ := startupClient(t, backend, rooms, 0)
	result, err := startupOnly(t, api, StartupHistoryOptions{})
	if err != nil || result.Pages != 2 || result.HistoryPending != 0 || result.Logs != 0 {
		t.Fatalf("batch cap %+v %v", result, err)
	}
	if len(api.historyStore.bulkSnapshot()) != 0 {
		t.Fatal("empty EOF invented acquired messages")
	}
	waitBackends(t, backend)
}
func TestStartupBulkStopsNonProgressAndRetainsCursor(t *testing.T) {
	backend := newScriptedBackend(t, false, testloco.Expect("MCHATLOGS", checkBulk([]int64{42}, []int64{0}), statusDocument(bson.E{Key: "eof", Value: false})))
	api, _ := startupClient(t, backend, []bson.Raw{bulkRoom(t, 42, 103)}, 0)
	// A non-progressing page leaves the room for a later connect instead of
	// failing the connect that carries catch-up.
	if result, err := startupOnly(t, api, StartupHistoryOptions{}); err != nil || result.HistoryPending != 1 {
		t.Fatal(result, err)
	}
	if len(api.historyStore.bulkSnapshot()) != 0 {
		t.Fatal("non-progress advanced acquisition")
	}
	waitBackends(t, backend)
}
func TestStartupBulkBudgetResumeUsesAcquiredCursor(t *testing.T) {
	backend := newScriptedBackend(t, false,
		testloco.Expect("MCHATLOGS", checkBulk([]int64{42}, []int64{0}), statusDocument(bson.E{Key: "eof", Value: false}, bson.E{Key: "chatLogs", Value: bson.A{bulkLog(42, 101)}})),
		testloco.Expect("MCHATLOGS", checkBulk([]int64{42}, []int64{101}), statusDocument(bson.E{Key: "eof", Value: true}, bson.E{Key: "chatLogs", Value: bson.A{bulkLog(42, 103)}})),
	)
	api, dir := startupClient(t, backend, []bson.Raw{bulkRoom(t, 42, 103)}, 0)
	first, err := startupOnly(t, api, StartupHistoryOptions{MaxPages: 1})
	if err != nil || first.HistoryPending != 1 {
		t.Fatal(first, err)
	}
	api.historyStore = reopenTestHistoryStore(t, dir)
	next, err := startupOnly(t, api, StartupHistoryOptions{MaxPages: 1})
	if err != nil || next.HistoryPending != 0 {
		t.Fatal(next, err)
	}
	waitBackends(t, backend)
}
func TestStartupBulkReconstructsCursorFromDurablePage(t *testing.T) {
	store, dir := testHistoryStore(t)
	if err := store.save(syncmsg.Request{ChatID: 42, Cur: 0, Max: 103}, syncmsg.Response{ChatLogs: []bson.Raw{historyStoreRaw(t, 101, "persisted before journal")}}); err != nil {
		t.Fatal(err)
	}
	reopened := reopenTestHistoryStore(t, dir)
	if reopened.bulkSnapshot()[42] != 101 {
		t.Fatal("restart lost acquired prefix without bulk journal")
	}
}

func TestStartupBulkDoesNotSkipDisconnectedCachedSuffix(t *testing.T) {
	for _, legacyJournal := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy_journal_%t", legacyJournal), func(t *testing.T) {
			backend := newScriptedBackend(t, false, testloco.Expect("MCHATLOGS",
				checkBulk([]int64{42}, []int64{0}),
				statusDocument(bson.E{Key: "eof", Value: true}, bson.E{Key: "chatLogs", Value: bson.A{
					bulkLog(42, 101), bulkLog(42, 102), bulkLog(42, 103),
				}})))
			api, dir := startupClient(t, backend, []bson.Raw{bulkRoom(t, 42, 103)}, 0)
			if err := api.historyStore.save(syncmsg.Request{ChatID: 42, Cur: 102, Max: 103},
				syncmsg.Response{ChatLogs: []bson.Raw{mustBSON(bulkLog(42, 103))}}); err != nil {
				t.Fatal(err)
			}
			if legacyJournal {
				// Previously written journals can record the highest acquired identity
				// even though its page does not connect to the beginning of the window.
				if err := api.historyStore.commitBulk(map[int64]int64{42: 103}); err != nil {
					t.Fatal(err)
				}
			}
			api.historyStore = reopenTestHistoryStore(t, dir)
			got, err := startupOnly(t, api, StartupHistoryOptions{MaxPages: 1})
			if err != nil || got.Pages != 1 || got.HistoryPending != 0 {
				t.Fatalf("disconnected suffix skipped acquisition: %+v %v", got, err)
			}
			api.historyStore = reopenTestHistoryStore(t, dir)
			page, err := api.ReadHistoryPage(t.Context(), 42, 0, 103, 300)
			if err != nil || !page.Complete || len(page.Events) != 3 {
				t.Fatalf("recovered prefix: %+v %v", page, err)
			}
			for i, ev := range page.Events {
				message, ok := ev.(events.TextMessage)
				if !ok || message.LogID != int64(101+i) || message.Message != "synthetic acquired" {
					t.Fatalf("recovered identity/order/content at %d: %+v", i, ev)
				}
			}
			again, err := startupOnly(t, api, StartupHistoryOptions{MaxPages: 1})
			if err != nil || again.Pages != 0 || again.HistoryPending != 0 {
				t.Fatalf("satisfied restart reacquired history: %+v %v", again, err)
			}
			waitBackends(t, backend)
		})
	}
}

func TestStartupBulkBudgetResumeStopsAtGapBeforeCachedSuffix(t *testing.T) {
	backend := newScriptedBackend(t, false,
		testloco.Expect("MCHATLOGS", checkBulk([]int64{42}, []int64{0}),
			statusDocument(bson.E{Key: "eof", Value: false}, bson.E{Key: "chatLogs", Value: bson.A{bulkLog(42, 101)}})),
		testloco.Expect("MCHATLOGS", checkBulk([]int64{42}, []int64{101}),
			statusDocument(bson.E{Key: "eof", Value: true}, bson.E{Key: "chatLogs", Value: bson.A{bulkLog(42, 102)}})),
	)
	api, dir := startupClient(t, backend, []bson.Raw{bulkRoom(t, 42, 103)}, 0)
	if err := api.historyStore.save(syncmsg.Request{ChatID: 42, Cur: 102, Max: 103},
		syncmsg.Response{ChatLogs: []bson.Raw{mustBSON(bulkLog(42, 103))}}); err != nil {
		t.Fatal(err)
	}
	first, err := startupOnly(t, api, StartupHistoryOptions{MaxPages: 1})
	if err != nil || first.Pages != 1 || first.HistoryPending != 1 {
		t.Fatal(first, err)
	}
	api.historyStore = reopenTestHistoryStore(t, dir)
	if cursor := api.historyStore.bulkSnapshot()[42]; cursor != 101 {
		t.Fatalf("budget restart skipped unresolved gap: %d", cursor)
	}
	next, err := startupOnly(t, api, StartupHistoryOptions{MaxPages: 1})
	if err != nil || next.Pages != 1 || next.HistoryPending != 0 {
		t.Fatal(next, err)
	}
	api.historyStore = reopenTestHistoryStore(t, dir)
	page, err := api.ReadHistoryPage(t.Context(), 42, 0, 103, 300)
	if err != nil || !page.Complete || len(page.Events) != 3 || api.historyStore.bulkSnapshot()[42] != 103 {
		t.Fatalf("connected prefix/suffix recovery: %+v %v", page, err)
	}
	again, err := startupOnly(t, api, StartupHistoryOptions{MaxPages: 1})
	if err != nil || again.Pages != 0 {
		t.Fatal(again, err)
	}
	waitBackends(t, backend)
}

func TestStartupBulkOptionsFailClosed(t *testing.T) {
	backend := newScriptedBackend(t, false)
	api, _ := startupClient(t, backend, []bson.Raw{bulkRoom(t, 42, 10)}, 0)
	for _, options := range []StartupHistoryOptions{{MaxPages: -1}, {MaxPages: 1001}, {MaximumDuration: -time.Second}, {MaximumDuration: 2 * time.Hour}} {
		if _, err := api.CatchUpAll(t.Context(), CatchUpOptions{StartupHistory: &options}); !errors.Is(err, ErrProtocol) {
			t.Fatal(options, err)
		}
	}
	waitBackends(t, backend)
}

func TestStartupBulkConcurrentInvocationRechecksDurableEligibility(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	backend := newScriptedBackend(t, false, testloco.Expect("MCHATLOGS", func(raw bson.Raw) error { close(entered); <-release; return checkBulk([]int64{42}, []int64{0})(raw) }, statusDocument(bson.E{Key: "eof", Value: true}, bson.E{Key: "chatLogs", Value: bson.A{bulkLog(42, 10)}})))
	api, _ := startupClient(t, backend, []bson.Raw{bulkRoom(t, 42, 10)}, 0)
	done := make(chan error, 2)
	go func() { _, err := startupOnly(t, api, StartupHistoryOptions{}); done <- err }()
	<-entered
	go func() {
		result, err := startupOnly(t, api, StartupHistoryOptions{})
		if err == nil && result.Pages != 0 {
			err = fmt.Errorf("warm invocation fetched %d pages", result.Pages)
		}
		done <- err
	}()
	waitForCondition(t, func() bool { api.mu.Lock(); defer api.mu.Unlock(); return api.historyStartWait == 2 })
	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	waitBackends(t, backend)
}

func TestStartupBulkJournalCannotSkipMissingAcquiredPage(t *testing.T) {
	store, dir := testHistoryStore(t)
	req := syncmsg.Request{ChatID: 42, Cur: 0, Max: 103}
	if err := store.save(req, syncmsg.Response{ChatLogs: []bson.Raw{historyStoreRaw(t, 101, "synthetic")}}); err != nil {
		t.Fatal(err)
	}
	if err := store.commitBulk(map[int64]int64{42: 101}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, historyPageName(req))); err != nil {
		t.Fatal(err)
	}
	state := reusableTestState()
	if _, err := openHistoryStore(dir, state.Credentials.UserID, state.Identity.DeviceUUID); err == nil {
		t.Fatal("bulk cursor skipped missing acquired page")
	}
}

func TestStartupBulkCapturesLoginFloor(t *testing.T) {
	dialers, backends := scriptedLogin(t, testloco.Expect("LOGINLIST", nil, statusDocument(bson.E{Key: "chatDatas", Value: bson.A{}}, bson.E{Key: "eof", Value: true}, bson.E{Key: "minLogId", Value: int64(123)})), testloco.HoldOpen())
	s, err := connectSession(t.Context(), reusableTestState(), sessionConfig{dialers: dialers, ping: realtimePingOptions()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if s.loginMinLogID != 123 {
		t.Fatal("lost global startup floor")
	}
	_ = s.Close()
	waitBackends(t, backends...)
}
