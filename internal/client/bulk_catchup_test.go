package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/events"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// The observed connect-traffic fixture names log identities "chat:index",
// where index orders every identity seen for that room. bulkFixtureFloor
// stands for the login minLogId; the per-room bases place each identity on
// the right side of it as the fixture's *_below_minLogId flags record.
const bulkFixtureFloor = int64(100_000)

var bulkFixtureBases = map[int64]int64{
	5001: 200_000,
	5002: 50_000,
	5003: 300_000,
	5004: 60_000,
	5005: 70_000,
	// 5006:0 is below the floor and 5006:1 is the first identity above it.
	5006: bulkFixtureFloor - 10,
	5007: 400_000,
}

// bulkFixtureID maps a fixture identity to a synthetic, sparse log ID.
// "unseen" (an older identity the server did not return) maps to index -1.
func bulkFixtureID(t *testing.T, chatID int64, identity string) int64 {
	t.Helper()
	index := -1
	if identity != "unseen" && identity != "zero" {
		prefix := fmt.Sprintf("%d:", chatID)
		value, err := strconv.Atoi(strings.TrimPrefix(identity, prefix))
		if !strings.HasPrefix(identity, prefix) || err != nil {
			t.Fatalf("fixture identity %q is not in chat %d", identity, chatID)
		}
		index = value
	}
	return bulkFixtureBases[chatID] + int64(index)*10
}

type bulkFixtureRun struct {
	ChatID    int64  `json:"chatId"`
	First     string `json:"first"`
	Last      string `json:"last"`
	Logs      int    `json:"logs"`
	FirstPrev string `json:"first_prev"`
}

type bulkFixture struct {
	Provenance struct {
		Kind string `json:"kind"`
	} `json:"provenance"`
	Rooms []struct {
		ChatID       int64  `json:"chatId"`
		Committed    string `json:"committed"`
		LoginCeiling string `json:"login_ceiling"`
		Behind       bool   `json:"behind"`
	} `json:"rooms"`
	Exchanges []struct {
		Method string `json:"method"`
		EOF    bool   `json:"eof"`
		Runs   []bulkFixtureRun
	} `json:"exchanges"`
}

func loadBulkFixture(t *testing.T) bulkFixture {
	t.Helper()
	data, err := os.ReadFile("../../research/fixtures/connect-traffic/mchatlogs-catchup-observed.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture bulkFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Provenance.Kind != "observed" || len(fixture.Exchanges) != 11 {
		t.Fatalf("unexpected fixture shape: %+v", fixture.Provenance)
	}
	return fixture
}

// chainLog is one MCHATLOGS chat log with its predecessor identity.
func chainLog(chatID, prevID, logID int64) bson.D {
	return bson.D{
		{Key: "chatId", Value: chatID},
		{Key: "logId", Value: logID},
		{Key: "prevId", Value: prevID},
		{Key: "type", Value: int32(1)},
		{Key: "message", Value: fmt.Sprintf("synthetic %d", logID)},
	}
}

// fixtureRunLogs expands one observed run into contiguous chained logs.
// firstPrev overrides the run's predecessor when the fixture recorded
// "unseen".
func fixtureRunLogs(t *testing.T, run bulkFixtureRun, firstPrev int64) bson.A {
	t.Helper()
	first := bulkFixtureID(t, run.ChatID, run.First)
	last := bulkFixtureID(t, run.ChatID, run.Last)
	if int((last-first)/10)+1 != run.Logs {
		t.Fatalf("fixture run %+v is not contiguous", run)
	}
	prev := firstPrev
	logs := bson.A{}
	for id := first; id <= last; id += 10 {
		logs = append(logs, chainLog(run.ChatID, prev, id))
		prev = id
	}
	return logs
}

func bulkReply(eof bool, logs ...bson.A) bson.D {
	all := bson.A{}
	for _, run := range logs {
		all = append(all, run...)
	}
	return statusDocument(bson.E{Key: "eof", Value: eof}, bson.E{Key: "chatLogs", Value: all})
}

// catchUpClient connects a client whose checkpoint has committed each
// chat's position and records each chat's login ceiling.
func catchUpClient(t *testing.T, backend *testloco.Backend, committed, ceilings map[int64]int64, floor int64) (*Client, *continuity.Store) {
	t.Helper()
	checkpoint := testCheckpoint(t)
	token := int64(7)
	known := make([]continuity.ChatTarget, 0, len(ceilings))
	for chatID, ceiling := range ceilings {
		known = append(known, continuity.ChatTarget{ChatID: chatID, MaxLogID: ceiling})
	}
	if err := checkpoint.InstallSession(&token, nil, known, nil, true); err != nil {
		t.Fatal(err)
	}
	for chatID, logID := range committed {
		if _, err := checkpoint.CommitMessage(chatID, logID); err != nil {
			t.Fatal(err)
		}
	}
	// Committing raises a known target; restore the login ceilings.
	if err := checkpoint.InstallSession(&token, nil, known, nil, true); err != nil {
		t.Fatal(err)
	}
	api := testContinuityClient(t, checkpoint, backend)
	if err := api.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	api.session.loginMinLogID = floor
	return api, checkpoint
}

func roomLogIDs(t *testing.T, room CaughtUpRoom) []int64 {
	t.Helper()
	ids := make([]int64, 0, len(room.Events))
	for _, event := range room.Events {
		chatID, logID, ok := events.MessagePosition(event)
		if !ok || chatID != room.ChatID {
			t.Fatalf("event %#v is not positioned in chat %d", event, room.ChatID)
		}
		ids = append(ids, logID)
	}
	return ids
}

func commitRooms(t *testing.T, api *Client, result CatchUpResult) {
	t.Helper()
	for _, room := range result.Rooms {
		for _, event := range room.Events {
			if err := api.CommitEvent(event); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func fixtureIDs(t *testing.T, chatID int64, from, through int) []int64 {
	t.Helper()
	ids := []int64{}
	for index := from; index <= through; index++ {
		ids = append(ids, bulkFixtureID(t, chatID, fmt.Sprintf("%d:%d", chatID, index)))
	}
	return ids
}

// CT-LIVE-001: from the bridge's committed positions one MCHATLOGS returned
// every missed log that per-room SYNCMSG needed three requests for. Catch-up
// sends that one request and no SYNCMSG; the log past the login ceiling is
// not delivered.
func TestBulkCatchUpCommittedPositionsSendOneMCHATLOGS(t *testing.T) {
	fixture := loadBulkFixture(t)
	committed, ceilings := map[int64]int64{}, map[int64]int64{}
	for _, room := range fixture.Rooms {
		committed[room.ChatID] = bulkFixtureID(t, room.ChatID, room.Committed)
		ceilings[room.ChatID] = bulkFixtureID(t, room.ChatID, room.LoginCeiling)
	}
	exchange := fixture.Exchanges[0]
	if exchange.Method != "MCHATLOGS" || !exchange.EOF || len(exchange.Runs) != 2 {
		t.Fatalf("fixture exchange 0 = %+v", exchange)
	}
	var runs []bson.A
	for _, run := range exchange.Runs {
		runs = append(runs, fixtureRunLogs(t, run, bulkFixtureID(t, run.ChatID, run.FirstPrev)))
	}
	backend := newScriptedBackend(t, false, testloco.Expect("MCHATLOGS",
		checkBulk([]int64{5003, 5006}, []int64{committed[5003], committed[5006]}),
		bulkReply(true, runs...)))
	api, checkpoint := catchUpClient(t, backend, committed, ceilings, bulkFixtureFloor)

	result, err := api.CatchUpAll(t.Context(), CatchUpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pages != 1 || len(result.Rooms) != 2 {
		t.Fatalf("result = %+v", result)
	}
	want := map[int64][]int64{5003: fixtureIDs(t, 5003, 6, 8), 5006: fixtureIDs(t, 5006, 1, 2)}
	for _, room := range result.Rooms {
		if room.Skipped || room.Unresolved || !reflect.DeepEqual(roomLogIDs(t, room), want[room.ChatID]) {
			t.Fatalf("room %d = %+v, want logs %v", room.ChatID, room, want[room.ChatID])
		}
	}
	if result.Rooms[0].ChatID != 5003 || result.Rooms[1].ChatID != 5006 {
		t.Fatal("rooms are not in ascending chat order")
	}
	if checkpoint.IsCommitted(5003, ceilings[5003]) {
		t.Fatal("catch-up committed before delivery")
	}
	commitRooms(t, api, result)
	for _, chatID := range []int64{5003, 5006} {
		if got := checkpoint.Snapshot().CommittedMax(chatID); got != ceilings[chatID] {
			t.Fatalf("chat %d committed through %d, want ceiling %d", chatID, got, ceilings[chatID])
		}
	}
	if gaps := checkpoint.Snapshot().HistoryGaps; len(gaps) != 0 {
		t.Fatalf("complete catch-up recorded gaps %+v", gaps)
	}
	waitBackends(t, backend)
}

// CT-LIVE-002/003: the 80-log cap is per response across rooms. The
// boundary room stays pending and completes on the next page; rooms below
// the boundary retire; rooms whose ceiling is below the login floor are
// never requested.
func TestBulkCatchUpFollowsTheObservedFivePageChain(t *testing.T) {
	fixture := loadBulkFixture(t)
	pages := fixture.Exchanges[4:9]
	// The probe requested from zero. Catch-up requests from committed
	// positions, so each room's committed position is its first run's
	// predecessor.
	committed := map[int64]int64{
		5001: bulkFixtureID(t, 5001, "unseen"),
		5003: bulkFixtureID(t, 5003, "unseen"),
		5006: bulkFixtureID(t, 5006, "5006:0"),
		5007: bulkFixtureID(t, 5007, "unseen"),
	}
	ceilings := map[int64]int64{}
	for _, room := range fixture.Rooms {
		ceilings[room.ChatID] = bulkFixtureID(t, room.ChatID, room.LoginCeiling)
	}
	cursor := map[int64]int64{}
	for chatID, position := range committed {
		cursor[chatID] = position
	}
	pending := []int64{5001, 5003, 5006, 5007}
	var steps []testloco.Step
	for i, page := range pages {
		sinces := make([]int64, len(pending))
		for j, chatID := range pending {
			sinces[j] = cursor[chatID]
		}
		var runs []bson.A
		for _, run := range page.Runs {
			runs = append(runs, fixtureRunLogs(t, run, cursor[run.ChatID]))
			cursor[run.ChatID] = bulkFixtureID(t, run.ChatID, run.Last)
		}
		steps = append(steps, testloco.Expect("MCHATLOGS", checkBulk(append([]int64(nil), pending...), sinces), bulkReply(page.EOF, runs...)))
		if i == 3 {
			// Page 4 ends in 5003: 5001 retires, 5003 is the boundary room.
			pending = []int64{5003, 5006, 5007}
		}
	}
	backend := newScriptedBackend(t, false, steps...)
	api, checkpoint := catchUpClient(t, backend, committed, ceilings, bulkFixtureFloor)

	result, err := api.CatchUpAll(t.Context(), CatchUpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pages != 5 {
		t.Fatalf("pages = %d, want 5", result.Pages)
	}
	want := map[int64][]int64{
		5001: fixtureIDs(t, 5001, 0, 309),
		5003: fixtureIDs(t, 5003, 0, 8),
		5006: fixtureIDs(t, 5006, 1, 2),
		5007: fixtureIDs(t, 5007, 0, 2),
	}
	if len(result.Rooms) != len(want) {
		t.Fatalf("rooms = %d, want %d", len(result.Rooms), len(want))
	}
	for _, room := range result.Rooms {
		if room.Skipped || room.Unresolved || !reflect.DeepEqual(roomLogIDs(t, room), want[room.ChatID]) {
			t.Fatalf("room %d: skipped=%t unresolved=%t logs=%d", room.ChatID, room.Skipped, room.Unresolved, len(room.Events))
		}
	}
	commitRooms(t, api, result)
	for chatID := range want {
		if got := checkpoint.Snapshot().CommittedMax(chatID); got != ceilings[chatID] {
			t.Fatalf("chat %d committed through %d, want %d", chatID, got, ceilings[chatID])
		}
	}
	waitBackends(t, backend)
}

// A run whose first prevId is above the cursor means the server skipped
// identities. The skipped interval is recorded as a gap and the rest of the
// run is still delivered.
func TestBulkCatchUpRecordsSkippedPredecessorAndDeliversRemainder(t *testing.T) {
	backend := newScriptedBackend(t, false, testloco.Expect("MCHATLOGS",
		checkBulk([]int64{42}, []int64{100}),
		bulkReply(true, bson.A{chainLog(42, 104, 105), chainLog(42, 105, 107)})))
	api, checkpoint := catchUpClient(t, backend, map[int64]int64{42: 100}, map[int64]int64{42: 107}, 0)

	result, err := api.CatchUpAll(t.Context(), CatchUpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rooms) != 1 || !result.Rooms[0].Skipped || result.Rooms[0].Unresolved {
		t.Fatalf("result = %+v", result)
	}
	if got := roomLogIDs(t, result.Rooms[0]); !reflect.DeepEqual(got, []int64{105, 107}) {
		t.Fatalf("delivered %v", got)
	}
	if gaps := checkpoint.Snapshot().HistoryGaps; len(gaps) != 1 || gaps[0] != (continuity.HistoryGap{ChatID: 42, FromLogID: 101, ToLogID: 104}) {
		t.Fatalf("gaps = %+v", gaps)
	}
	waitBackends(t, backend)
}

// A predecessor below the cursor, a missing prevId or a broken chain inside
// a run is not guessed at: the room's interval is recorded as unresolved
// and nothing from that run is delivered.
func TestBulkCatchUpBrokenChainIsUnresolved(t *testing.T) {
	missingPrev := bson.D{{Key: "chatId", Value: int64(42)}, {Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "no predecessor"}}
	for name, run := range map[string]bson.A{
		"prev below cursor": {chainLog(42, 99, 101)},
		"missing prev":      {missingPrev},
		"broken chain":      {chainLog(42, 100, 101), chainLog(42, 102, 103)},
	} {
		t.Run(name, func(t *testing.T) {
			backend := newScriptedBackend(t, false, testloco.Expect("MCHATLOGS", checkBulk([]int64{42}, []int64{100}), bulkReply(true, run)))
			api, checkpoint := catchUpClient(t, backend, map[int64]int64{42: 100}, map[int64]int64{42: 103}, 0)
			result, err := api.CatchUpAll(t.Context(), CatchUpOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Rooms) != 1 || !result.Rooms[0].Unresolved || len(result.Rooms[0].Events) != 0 {
				t.Fatalf("result = %+v", result)
			}
			if gaps := checkpoint.Snapshot().HistoryGaps; len(gaps) != 1 || gaps[0] != (continuity.HistoryGap{ChatID: 42, FromLogID: 101, ToLogID: 103}) {
				t.Fatalf("gaps = %+v", gaps)
			}
			waitBackends(t, backend)
		})
	}
}

// An empty eof=false page makes no progress. Catch-up stops without a
// second request and records every pending room's interval.
func TestBulkCatchUpStopsOnEmptyNonFinalPage(t *testing.T) {
	backend := newScriptedBackend(t, false, testloco.Expect("MCHATLOGS",
		checkBulk([]int64{42, 43}, []int64{100, 200}),
		bulkReply(false)))
	api, checkpoint := catchUpClient(t, backend, map[int64]int64{42: 100, 43: 200}, map[int64]int64{42: 105, 43: 205}, 0)

	result, err := api.CatchUpAll(t.Context(), CatchUpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pages != 1 || len(result.Rooms) != 2 || !result.Rooms[0].Unresolved || !result.Rooms[1].Unresolved {
		t.Fatalf("result = %+v", result)
	}
	want := []continuity.HistoryGap{{ChatID: 42, FromLogID: 101, ToLogID: 105}, {ChatID: 43, FromLogID: 201, ToLogID: 205}}
	if gaps := checkpoint.Snapshot().HistoryGaps; !reflect.DeepEqual(gaps, want) {
		t.Fatalf("gaps = %+v, want %+v", gaps, want)
	}
	waitBackends(t, backend)
}

// A retired room whose cursor did not reach its ceiling (eof=true without
// the rest of its interval) is unresolved after the logs it did receive.
func TestBulkCatchUpRetiredShortRoomKeepsDeliveredPrefix(t *testing.T) {
	backend := newScriptedBackend(t, false, testloco.Expect("MCHATLOGS",
		checkBulk([]int64{42}, []int64{100}),
		bulkReply(true, bson.A{chainLog(42, 100, 101)})))
	api, checkpoint := catchUpClient(t, backend, map[int64]int64{42: 100}, map[int64]int64{42: 105}, 0)

	result, err := api.CatchUpAll(t.Context(), CatchUpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rooms) != 1 || !result.Rooms[0].Unresolved || !reflect.DeepEqual(roomLogIDs(t, result.Rooms[0]), []int64{101}) {
		t.Fatalf("result = %+v", result)
	}
	if gaps := checkpoint.Snapshot().HistoryGaps; len(gaps) != 1 || gaps[0] != (continuity.HistoryGap{ChatID: 42, FromLogID: 102, ToLogID: 105}) {
		t.Fatalf("gaps = %+v", gaps)
	}
	waitBackends(t, backend)
}

// The page cap is account-wide. When it is reached, every pending room's
// remaining interval is recorded so a live commit cannot skip it.
func TestBulkCatchUpPageCapRecordsPendingRooms(t *testing.T) {
	steps := make([]testloco.Step, 0, maxBulkCatchUpPages)
	for page := range int64(maxBulkCatchUpPages) {
		steps = append(steps, testloco.Expect("MCHATLOGS",
			checkBulk([]int64{42}, []int64{1000 + page}),
			bulkReply(false, bson.A{chainLog(42, 1000+page, 1001+page)})))
	}
	backend := newScriptedBackend(t, false, steps...)
	api, checkpoint := catchUpClient(t, backend, map[int64]int64{42: 1000}, map[int64]int64{42: 5000}, 0)

	result, err := api.CatchUpAll(t.Context(), CatchUpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pages != maxBulkCatchUpPages || len(result.Rooms) != 1 || !result.Rooms[0].Unresolved || len(result.Rooms[0].Events) != maxBulkCatchUpPages {
		t.Fatalf("pages=%d rooms=%+v", result.Pages, result.Rooms)
	}
	want := continuity.HistoryGap{ChatID: 42, FromLogID: 1001 + maxBulkCatchUpPages, ToLogID: 5000}
	if gaps := checkpoint.Snapshot().HistoryGaps; len(gaps) != 1 || gaps[0] != want {
		t.Fatalf("gaps = %+v, want %+v", gaps, want)
	}
	waitBackends(t, backend)
}

// CT-LIVE-004: a room whose ceiling is below the login floor is not
// selected by native catch-up. It sends no request and records the gap.
func TestBulkCatchUpFloorRoomSendsNoRequestAndRecordsGap(t *testing.T) {
	backend := newScriptedBackend(t, false, testloco.Expect("MCHATLOGS",
		checkBulk([]int64{43}, []int64{200}),
		bulkReply(true, bson.A{chainLog(43, 200, 201)})))
	api, checkpoint := catchUpClient(t, backend, map[int64]int64{42: 100, 43: 200}, map[int64]int64{42: 105, 43: 201}, 150)

	result, err := api.CatchUpAll(t.Context(), CatchUpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rooms) != 2 || result.Rooms[0].ChatID != 42 || !result.Rooms[0].Unresolved || result.Rooms[1].Unresolved {
		t.Fatalf("result = %+v", result)
	}
	if gaps := checkpoint.Snapshot().HistoryGaps; len(gaps) != 1 || gaps[0] != (continuity.HistoryGap{ChatID: 42, FromLogID: 101, ToLogID: 105}) {
		t.Fatalf("gaps = %+v", gaps)
	}
	waitBackends(t, backend)
}

// A transport or status failure aborts catch-up without queuing any
// commit, so the bootstrap that called it can fail closed.
func TestBulkCatchUpFailureQueuesNothing(t *testing.T) {
	backend := newScriptedBackend(t, false,
		testloco.Expect("MCHATLOGS", checkBulk([]int64{42}, []int64{100}), bulkReply(false, bson.A{chainLog(42, 100, 101)})),
		testloco.Expect("MCHATLOGS", checkBulk([]int64{42}, []int64{101}), bson.D{{Key: "status", Value: int32(-500)}}),
	)
	api, checkpoint := catchUpClient(t, backend, map[int64]int64{42: 100}, map[int64]int64{42: 105}, 0)
	if _, err := api.CatchUpAll(t.Context(), CatchUpOptions{}); err == nil {
		t.Fatal("status failure was not reported")
	}
	if err := api.CommitEvent(events.TextMessage{ChatID: 42, LogID: 101}); err == nil {
		t.Fatal("a failed catch-up queued a commit")
	}
	if gaps := checkpoint.Snapshot().HistoryGaps; len(gaps) != 0 {
		t.Fatalf("a failed catch-up recorded gaps %+v", gaps)
	}
	waitBackends(t, backend)
}

// With startup_history, the history acquisition rides the same chain:
// one MCHATLOGS carries the catch-up room from its committed position and
// the acquisition room from its acquired cursor.
func TestBulkCatchUpCarriesStartupHistoryOnTheSameChain(t *testing.T) {
	backend := newScriptedBackend(t, false, testloco.Expect("MCHATLOGS",
		checkBulk([]int64{42, 43}, []int64{100, 0}),
		bulkReply(true, bson.A{chainLog(42, 100, 101)}, bson.A{bulkLog(43, 201), bulkLog(43, 203)})))
	api, _ := catchUpClient(t, backend, map[int64]int64{42: 100}, map[int64]int64{42: 101, 43: 203}, 0)
	api.historyStore, _ = testHistoryStore(t)
	api.historyAdmission = testAdmission(t, "")
	api.session.loginCursor = fullChatListSession(t, []bson.Raw{bulkRoom(t, 42, 101), bulkRoom(t, 43, 203)}).loginCursor

	result, err := api.CatchUpAll(t.Context(), CatchUpOptions{StartupHistory: &StartupHistoryOptions{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pages != 1 || result.HistoryPending != 0 || len(result.Rooms) != 1 || !reflect.DeepEqual(roomLogIDs(t, result.Rooms[0]), []int64{101}) {
		t.Fatalf("result = %+v", result)
	}
	if cursors := api.historyStore.bulkSnapshot(); cursors[43] != 203 {
		t.Fatalf("acquired cursors = %v", cursors)
	}
	page, err := api.ReadHistoryPage(t.Context(), 43, 0, 203, 300)
	if err != nil || !page.Complete || len(page.Events) != 2 {
		t.Fatalf("acquired history = %+v %v", page, err)
	}
	waitBackends(t, backend)
}

func TestBulkCatchUpRejectsLogOutsideTheRequest(t *testing.T) {
	backend := newScriptedBackend(t, false, testloco.Expect("MCHATLOGS",
		checkBulk([]int64{42}, []int64{100}),
		bulkReply(true, bson.A{chainLog(44, 100, 101)})))
	api, _ := catchUpClient(t, backend, map[int64]int64{42: 100}, map[int64]int64{42: 101}, 0)
	if _, err := api.CatchUpAll(t.Context(), CatchUpOptions{}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("err = %v, want protocol error", err)
	}
	waitBackends(t, backend)
}
