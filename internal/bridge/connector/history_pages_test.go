package connector

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

type windowSource struct {
	*fakeKakao
	pages    []client.StoredHistoryPage
	requests []client.StoredHistoryRequest
}

func (f *windowSource) ReadStoredHistory(_ context.Context, req client.StoredHistoryRequest) (client.StoredHistoryPage, error) {
	f.requests = append(f.requests, req)
	if len(f.pages) == 0 {
		return client.StoredHistoryPage{}, errors.New("unexpected local read")
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	return page, nil
}
func pageText(id int64, body string) events.TextMessage {
	return events.TextMessage{ChatID: testChatID, LogID: id, AuthorID: testOtherID, SentAt: 1000, Message: body}
}
func pageFramework(t *testing.T) (*KakaoClient, *bridgev2.Portal, *groupCreationMatrix) {
	t.Helper()
	kc, _, mx := newGroupCreationFramework(t)
	kc.login.Bridge.Network = &KakaoConnector{Config: Config{StartupHistory: true}}
	p, err := kc.login.Bridge.GetPortalByKey(t.Context(), makePortalKey(testChatID, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	return kc, p, mx
}
func TestHistoryCutoffPreservesSourceIDsInSameSecond(t *testing.T) {
	kc, p, _ := pageFramework(t)
	timestamp := time.Unix(1000, 0)
	for _, forward := range []bool{false, true} {
		anchor := &database.Message{Room: p.PortalKey, ID: makeMessageID(testChatID, 200), Timestamp: timestamp}
		id := int64(100)
		if forward {
			id = 300
		}
		messages := []*bridgev2.BackfillMessage{{ID: makeMessageID(testChatID, id), Timestamp: timestamp}}
		adaptHistoryCutoff(anchor, messages, forward)
		//nolint:staticcheck // Exercise the pinned framework's real cutoff.
		got := p.Internal().CutoffMessages(t.Context(), messages, false, forward, anchor)
		if len(got) != 1 || !got[0].Timestamp.Equal(timestamp) {
			t.Fatal("framework dropped same-second source identity or changed timestamp")
		}
	}
	_ = kc
}
func TestFetchHistoryPendingForwardSelectionSurvivesPartialDelivery(t *testing.T) {
	kc, p, mx := pageFramework(t)
	page := client.StoredHistoryPage{Complete: true, Events: []events.Event{pageText(100, "first"), pageText(200, "second")}}
	source := &windowSource{fakeKakao: &fakeKakao{chatInventory: []chatmeta.ChatData{{ChatID: testChatID, Type: "MultiChat", LastServerLogID: 200}}}, pages: []client.StoredHistoryPage{page, page}}
	kc.client = source
	first, err := kc.FetchMessages(t.Context(), bridgev2.FetchMessagesParams{Portal: p, Forward: true, Count: 50})
	if err != nil {
		t.Fatal(err)
	}
	mx.failMessage = "second"
	//nolint:staticcheck // Drive actual framework delivery and database writes.
	if err = p.Internal().SendBackfill(t.Context(), kc.login, first.Messages, true, false, false, first.CompleteCallback); err == nil {
		t.Fatal("synthetic send failure hidden")
	}
	anchor, err := kc.login.Bridge.DB.Message.GetFirstPortalMessage(t.Context(), p.PortalKey)
	if err != nil || anchor == nil {
		t.Fatal(anchor, err)
	}
	mx.failMessage = ""
	restarted := newKakaoClient(kc.login, newBridgeStore(kc.login.Bridge.DB), testSelfID, nil)
	restarted.client = source
	next, err := restarted.FetchMessages(t.Context(), bridgev2.FetchMessagesParams{Portal: p, Forward: true, Count: 1, AnchorMessage: anchor})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Messages) != 1 || next.Messages[0].ID != makeMessageID(testChatID, 200) || source.requests[1].After != 0 || source.requests[1].Limit != 50 {
		t.Fatal("restart forgot pending selection", source.requests, next)
	}
	//nolint:staticcheck // The adapter keeps the SDK cutoff from discarding this replay.
	kept := p.Internal().CutoffMessages(t.Context(), next.Messages, false, true, anchor)
	if len(kept) != 1 {
		t.Fatal("pending selection was cut off")
	}
	//nolint:staticcheck // Real framework confirms the missing message.
	if err = p.Internal().SendBackfill(t.Context(), kc.login, kept, true, false, false, next.CompleteCallback); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(mx.messageBodies, []string{"first", "second"}) {
		t.Fatal(mx.messageBodies)
	}
	row, err := kc.login.Bridge.DB.Message.GetFirstPortalMessage(t.Context(), p.PortalKey)
	if err != nil || row.ID != makeMessageID(testChatID, 100) || !row.Timestamp.Equal(time.Unix(1000, 0)) {
		t.Fatal("comparison anchor mutated persisted identity/timestamp", row, err)
	}
	batch, found, err := kc.loadHistoryBatch(t.Context(), "kakao:history-batch:1000:3000:true")
	if err != nil || !found || batch.Pending {
		t.Fatal(batch, found, err)
	}
}
func TestFetchHistoryMissingCacheDefersWithoutSourceCall(t *testing.T) {
	kc, p, mx := pageFramework(t)
	kc.login.Bridge.Matrix = &historyBatchMatrix{groupCreationMatrix: mx}
	source := &windowSource{fakeKakao: &fakeKakao{chatInventory: []chatmeta.ChatData{{ChatID: testChatID, Type: "MultiChat", LastServerLogID: 300}}}, pages: []client.StoredHistoryPage{{}}}
	kc.client = source
	anchor := &database.Message{Room: p.PortalKey, ID: makeMessageID(testChatID, 200), Timestamp: time.Unix(1000, 0)}
	got, err := kc.FetchMessages(t.Context(), bridgev2.FetchMessagesParams{Portal: p, Count: 50, AnchorMessage: anchor})
	if err != nil || !got.MoreRequiresSlowFetch || !got.HasMore || len(got.Messages) != 0 {
		t.Fatal(got, err)
	}
	if len(source.calls) != 0 {
		t.Fatal("local deferral used source")
	}
	if source.requests[0].Through != 200 || !source.requests[0].ExcludeThrough {
		t.Fatal(source.requests)
	}
}
func TestFetchHistoryRejectsForeignScopeAndUnsupportedThreads(t *testing.T) {
	kc, p, _ := pageFramework(t)
	for _, params := range []bridgev2.FetchMessagesParams{{Portal: p, Forward: true, Count: 0}, {Portal: p, Forward: true, Count: 50, ThreadRoot: "thread"}, {Portal: p, Forward: true, Count: 50, AnchorMessage: &database.Message{Room: p.PortalKey, ID: networkid.MessageID("4000:100")}}} {
		kc.client = &windowSource{fakeKakao: &fakeKakao{chatInventory: []chatmeta.ChatData{{ChatID: testChatID, Type: "MultiChat", LastServerLogID: 300}}}}
		if _, err := kc.FetchMessages(t.Context(), params); err == nil {
			t.Fatal("invalid scope accepted")
		}
	}
	for _, cursor := range []networkid.PaginationCursor{"v2:3000:100", "v1:4000:100", "v1:3000:0", "v1:3000:00100"} {
		if _, err := parseHistoryCursor(cursor, testChatID); err == nil {
			t.Fatal("bad cursor", cursor)
		}
	}
}

type historyBatchMatrix struct {
	*groupCreationMatrix
	attempts   int
	lost       bool
	applied    map[id.EventID]bool
	timestamps []int64
}

func (*historyBatchMatrix) GetCapabilities() *bridgev2.MatrixCapabilities {
	return &bridgev2.MatrixCapabilities{BatchSending: true}
}

func (m *historyBatchMatrix) GenerateDeterministicEventID(_ id.RoomID, _ networkid.PortalKey, message networkid.MessageID, part networkid.PartID) id.EventID {
	return id.EventID("$synthetic_" + string(message) + "_" + string(part))
}
func (m *historyBatchMatrix) BatchSend(_ context.Context, _ id.RoomID, req *mautrix.ReqBeeperBatchSend, _ []*bridgev2.MatrixSendExtra) (*mautrix.RespBeeperBatchSend, error) {
	m.attempts++
	if m.applied == nil {
		m.applied = map[id.EventID]bool{}
	}
	out := &mautrix.RespBeeperBatchSend{}
	for _, evt := range req.Events {
		m.applied[evt.ID] = true
		m.timestamps = append(m.timestamps, evt.Timestamp)
		out.EventIDs = append(out.EventIDs, evt.ID)
	}
	if m.lost {
		m.lost = false
		return nil, errors.New("synthetic lost batch response")
	}
	return out, nil
}
func TestFetchHistoryRealBackwardExecutorRetriesLostBatchWithoutIdentityLoss(t *testing.T) {
	kc, p, mx := pageFramework(t)
	batchMatrix := &historyBatchMatrix{groupCreationMatrix: mx, lost: true}
	kc.login.Bridge.Matrix = batchMatrix
	anchor := &database.Message{ID: makeMessageID(testChatID, 300), PartID: "", MXID: "$anchor", Room: p.PortalKey, Timestamp: time.Unix(1000, 0), SenderID: makeUserID(testOtherID), SenderMXID: "@kakao_2000:test", Metadata: newKakaoMessageMetadata(testChatID, 300, testOtherID, 1, "anchor", 0)}
	if err := kc.login.Bridge.DB.Message.Insert(t.Context(), anchor); err != nil {
		t.Fatal(err)
	}
	page := client.StoredHistoryPage{Complete: true, HasMore: true, Events: []events.Event{pageText(100, "first"), pageText(200, "second")}}
	source := &windowSource{fakeKakao: &fakeKakao{chatInventory: []chatmeta.ChatData{{ChatID: testChatID, Type: "MultiChat", LastServerLogID: 300}}}, pages: []client.StoredHistoryPage{page, page}}
	kc.client = source
	kc.login.Bridge.Config.Backfill.Queue.BatchSize = 50
	task := &database.BackfillTask{PortalKey: p.PortalKey, UserLoginID: kc.login.ID}
	//nolint:staticcheck // Execute actual fetch, cutoff, batch send and persistence.
	if _, err := p.Internal().DoBackwardsBackfill(t.Context(), kc.login, task, nil); err == nil {
		t.Fatal("lost response hidden")
	}
	rows, err := kc.store.messageParts(t.Context(), p.Receiver, makeMessageID(testChatID, 100))
	if err != nil || len(rows) != 0 {
		t.Fatal("unconfirmed mapping committed", rows, err)
	}
	restarted := newKakaoClient(kc.login, newBridgeStore(kc.login.Bridge.DB), testSelfID, nil)
	restarted.client = source
	kc.login.Client = restarted
	task = &database.BackfillTask{PortalKey: p.PortalKey, UserLoginID: kc.login.ID}
	//nolint:staticcheck // A fresh connector/database task recovers the private selection.
	if _, err = p.Internal().DoBackwardsBackfill(t.Context(), kc.login, task, nil); err != nil {
		t.Fatal(err)
	}
	if batchMatrix.attempts != 2 || len(batchMatrix.applied) != 2 || task.Cursor != historyCursor(testChatID, 100) {
		t.Fatal(batchMatrix.attempts, batchMatrix.applied, task.Cursor)
	}
	for _, timestamp := range batchMatrix.timestamps {
		if timestamp != 1000000 {
			t.Fatal("source timestamp changed", timestamp)
		}
	}
	for _, position := range []int64{100, 200} {
		rows, err = kc.store.messageParts(t.Context(), p.Receiver, makeMessageID(testChatID, position))
		if err != nil || len(rows) != 1 {
			t.Fatal(position, rows, err)
		}
	}
}

func TestFetchHistoryEmptyPortalIncludesInventoryLatest(t *testing.T) {
	kc, p, mx := pageFramework(t)
	kc.login.Bridge.Matrix = &historyBatchMatrix{groupCreationMatrix: mx}
	source := &windowSource{fakeKakao: &fakeKakao{chatInventory: []chatmeta.ChatData{{ChatID: testChatID, Type: "MultiChat", LastServerLogID: 300}}}, pages: []client.StoredHistoryPage{{Complete: true, Events: []events.Event{pageText(300, "latest")}}}}
	kc.client = source
	if _, err := kc.FetchMessages(t.Context(), bridgev2.FetchMessagesParams{Portal: p, Count: 50}); err != nil {
		t.Fatal(err)
	}
	if source.requests[0].ExcludeThrough {
		t.Fatal("empty portal excluded the inventory's latest identity")
	}
}

type unconfirmedHistoryStore struct{ store }

func (s unconfirmedHistoryStore) messageParts(ctx context.Context, receiver networkid.UserLoginID, message networkid.MessageID) ([]*database.Message, error) {
	if message == makeMessageID(testChatID, 100) {
		return nil, nil
	}
	return s.store.messageParts(ctx, receiver, message)
}
func TestFetchHistoryUnconfirmedMappingCannotFinishFrameworkTask(t *testing.T) {
	kc, p, mx := pageFramework(t)
	kc.login.Bridge.Matrix = &historyBatchMatrix{groupCreationMatrix: mx}
	kc.store = unconfirmedHistoryStore{kc.store}
	kc.login.Bridge.Config.Backfill.Queue.BatchSize = 50
	kc.client = &windowSource{fakeKakao: &fakeKakao{chatInventory: []chatmeta.ChatData{{ChatID: testChatID, Type: "MultiChat", LastServerLogID: 200}}}, pages: []client.StoredHistoryPage{{Complete: true, Events: []events.Event{pageText(100, "first"), pageText(200, "second")}}}}
	task := &database.BackfillTask{PortalKey: p.PortalKey, UserLoginID: kc.login.ID}
	//nolint:staticcheck // Framework sends successfully but the connector cannot confirm a mapping.
	if _, err := p.Internal().DoBackwardsBackfill(t.Context(), kc.login, task, nil); err != nil {
		t.Fatal(err)
	}
	if task.IsDone || task.QueueDone || task.Cursor != "" {
		t.Fatal("unconfirmed selection became a completed framework task")
	}
}

func TestStartupHistoryDeliveryCannotReportReadyWithPendingSelection(t *testing.T) {
	kc, _, _ := pageFramework(t)
	source := &fakeKakao{chatInventory: []chatmeta.ChatData{{ChatID: testChatID, Type: "MultiChat"}}}
	key := "kakao:history-batch:1000:3000:true"
	batch := historyBatch{Version: 1, Forward: true, Through: 200, First: 100, Limit: 50, Pending: true}
	if err := kc.saveHistoryBatch(t.Context(), key, batch); err != nil {
		t.Fatal(err)
	}
	if err := kc.checkStartupHistoryDelivery(t.Context(), source); err == nil {
		t.Fatal("ready with pending historical delivery")
	}
	batch.Pending = false
	if err := kc.saveHistoryBatch(t.Context(), key, batch); err != nil {
		t.Fatal(err)
	}
	if err := kc.checkStartupHistoryDelivery(t.Context(), source); err != nil {
		t.Fatal(err)
	}
}

func TestFetchHistoryUnavailableWindowDoesNotAcquireOrFinishTask(t *testing.T) {
	kc, p, mx := pageFramework(t)
	kc.login.Bridge.Matrix = &historyBatchMatrix{groupCreationMatrix: mx}
	observation := &client.HistoryUnavailable{MinLogID: 350, ObservedAt: time.Now(), RetryAfter: time.Now().Add(5 * time.Minute)}
	source := &windowSource{fakeKakao: &fakeKakao{chatInventory: []chatmeta.ChatData{{ChatID: testChatID, Type: "MultiChat", LastServerLogID: 300}}}, pages: []client.StoredHistoryPage{{Unavailable: observation}, {Unavailable: observation}}}
	kc.client = source
	response, err := kc.FetchMessages(t.Context(), bridgev2.FetchMessagesParams{Portal: p, Count: 50, AllowSlowFetch: true})
	var unavailable *client.HistoryUnavailableError
	if response != nil || !errors.As(err, &unavailable) || !errors.Is(err, client.ErrHistoryBelowFloor) || !unavailable.Observation.RetryAfter.Equal(observation.RetryAfter) {
		t.Fatal(response, err)
	}
	if len(source.calls) != 0 {
		t.Fatal("known unavailable interval reacquired", source.calls)
	}
	if _, found, err := kc.loadHistoryBatch(t.Context(), "kakao:history-batch:1000:3000:false"); err != nil || found {
		t.Fatal("unavailable interval became pending delivery", found, err)
	}
	kc.login.Bridge.Config.Backfill.Queue.BatchSize = 50
	task := &database.BackfillTask{PortalKey: p.PortalKey, UserLoginID: kc.login.ID}
	//nolint:staticcheck // Verify the actual framework preserves an unfinished task.
	if _, err = p.Internal().DoBackwardsBackfill(t.Context(), kc.login, task, nil); !errors.Is(err, client.ErrHistoryBelowFloor) {
		t.Fatal(err)
	}
	if task.IsDone || task.QueueDone || task.Cursor != "" || len(source.calls) != 0 {
		t.Fatal("unavailable window was retired or acquired", task, source.calls)
	}
}

type missingTailSource struct {
	*cachedProvisionSource
	after []int64
}

var errMissingTailProbe = errors.New("synthetic missing-tail stop")

func (s *missingTailSource) ReadHistoryPage(_ context.Context, _, after, _ int64, _ int) (client.HistoryPage, error) {
	s.after = append(s.after, after)
	return client.HistoryPage{}, errMissingTailProbe
}

func TestBackwardAcquisitionStartsAtPersistedCoverageAfterRestart(t *testing.T) {
	path := provisioningCacheFixture(t) // Production store covers (0,500].
	kc, p, mx := pageFramework(t)
	kc.login.Bridge.Matrix = &historyBatchMatrix{groupCreationMatrix: mx}
	for range 2 {
		sdk, err := client.OpenWithOptions(path, nil, client.OpenOptions{})
		if err != nil {
			t.Fatal(err)
		}
		source := &missingTailSource{cachedProvisionSource: &cachedProvisionSource{
			sdk:       sdk,
			fakeKakao: &fakeKakao{chatInventory: []chatmeta.ChatData{{ChatID: testChatID, Type: "MultiChat", LastServerLogID: 900}}},
		}}
		restarted := newKakaoClient(kc.login, newBridgeStore(kc.login.Bridge.DB), testSelfID, nil)
		restarted.client = source
		_, err = restarted.FetchMessages(t.Context(), bridgev2.FetchMessagesParams{Portal: p, Count: 50, AllowSlowFetch: true})
		if shutdownErr := sdk.Shutdown(t.Context()); shutdownErr != nil {
			t.Fatal(shutdownErr)
		}
		if !errors.Is(err, errMissingTailProbe) || !reflect.DeepEqual(source.after, []int64{500}) {
			t.Fatalf("missing-range demand rescanned acquired prefix: after=%v err=%v", source.after, err)
		}
	}
}

func TestBackwardCachedSuffixDeliversBeforeGapAndReplaysAfterRestart(t *testing.T) {
	path := provisioningCacheFixtureAfter(t, 300)
	kc, p, mx := pageFramework(t)
	batch := &historyBatchMatrix{groupCreationMatrix: mx, lost: true}
	kc.login.Bridge.Matrix = batch
	kc.login.Bridge.Config.Backfill.Queue.BatchSize = 50
	p.MXID = "!cached-suffix:test"
	for attempt := 0; attempt < 2; attempt++ {
		sdk, err := client.OpenWithOptions(path, nil, client.OpenOptions{})
		if err != nil {
			t.Fatal(err)
		}
		source := &cachedProvisionSource{sdk: sdk, fakeKakao: &fakeKakao{chatInventory: []chatmeta.ChatData{{ChatID: testChatID, Type: "MultiChat", LastServerLogID: 500}}}}
		restarted := newKakaoClient(kc.login, newBridgeStore(kc.login.Bridge.DB), testSelfID, nil)
		restarted.client = source
		kc.login.Client = restarted
		warm, warmErr := restarted.FetchMessages(t.Context(), bridgev2.FetchMessagesParams{Portal: p, Count: 50, AllowSlowFetch: true})
		if warmErr != nil || warm == nil || len(warm.Messages) != 2 || !warm.HasMore || warm.Messages[0].ID != makeMessageID(testChatID, 400) || warm.Messages[1].ID != makeMessageID(testChatID, 500) || source.acquisitions != 0 {
			t.Fatal("slow-fetch permission bypassed warm cache or lost source order", warm, warmErr)
		}
		task := &database.BackfillTask{PortalKey: p.PortalKey, UserLoginID: kc.login.ID}
		//nolint:staticcheck // Actual executor must deliver cached rows before fetching an older gap.
		_, err = p.Internal().DoBackwardsBackfill(t.Context(), kc.login, task, nil)
		if (attempt == 0 && err == nil) || (attempt == 1 && err != nil) {
			t.Fatal("unexpected batch outcome", err)
		}
		if source.acquisitions != 0 {
			t.Fatal("cached suffix triggered source acquisition")
		}
		if attempt == 1 {
			if task.IsDone || task.Cursor != historyCursor(testChatID, 400) {
				t.Fatal("older gap retired", task)
			}
			next, e := restarted.FetchMessages(t.Context(), bridgev2.FetchMessagesParams{Portal: p, Count: 50, Cursor: task.Cursor})
			if e != nil || !next.MoreRequiresSlowFetch || !next.HasMore || len(next.Messages) != 0 || source.acquisitions != 0 {
				t.Fatal("gap was skipped or acquired without slow demand", next, e)
			}
		}
		if err = sdk.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if batch.attempts != 2 || len(batch.applied) != 2 {
		t.Fatal("response-loss replay duplicated identities", batch.attempts, batch.applied)
	}
	for _, n := range []int64{400, 500} {
		rows, err := kc.store.messageParts(t.Context(), p.Receiver, makeMessageID(testChatID, n))
		if err != nil || len(rows) != 1 {
			t.Fatal("missing confirmed mapping", n, err)
		}
	}
}
