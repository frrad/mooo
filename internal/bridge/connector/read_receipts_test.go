package connector

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/events"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
)

type readReceiptFramework struct {
	kc     *KakaoClient
	bridge *bridgev2.Bridge
	login  *bridgev2.UserLogin
	matrix *frameworkMatrix
	queued int
}

// newReadReceiptFramework drives the connector through the real bridgev2
// remote-event handlers and a real SQLite bridge database.
func newReadReceiptFramework(t *testing.T) *readReceiptFramework {
	t.Helper()
	ctx := context.Background()
	// Separate identities tell a ghost's receipt from the double-puppeted user's.
	matrix := &frameworkMatrix{
		bot:   &recordingIntent{mxid: "@bot:test"},
		user:  &recordingIntent{mxid: "@owner:test"},
		ghost: &recordingIntent{mxid: "@ghost:test"},
	}
	bridge, raw := newFrameworkBridge(t, frameworkOpts{matrix: matrix})
	if _, err := raw.RawDB.ExecContext(ctx, `INSERT OR IGNORE INTO ghost (bridge_id,id,name,avatar_id,avatar_hash,avatar_mxc,name_set,avatar_set,contact_info_set,is_bot,identifiers,extra_profile,metadata) VALUES ('test','1000','self','','','',1,1,0,0,'[]',NULL,'{}')`); err != nil {
		t.Fatal(err)
	}
	user, err := bridge.GetUserByMXID(ctx, "@owner:test")
	if err != nil {
		t.Fatal(err)
	}
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{}, ID: makeUserLoginID(testSelfID), UserMXID: user.MXID}, Bridge: bridge, User: user, Log: zerolog.Nop()}
	f := &readReceiptFramework{bridge: bridge, login: login, matrix: matrix}
	f.kc = newKakaoClient(login, newBridgeStore(login.Bridge.DB), testSelfID, nil)
	f.kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		f.queued++
		portal, err := bridge.GetExistingPortalByKey(ctx, remote.GetPortalKey())
		if err != nil {
			return bridgev2.EventHandlingResultFailed.WithError(err)
		}
		if portal == nil {
			return bridgev2.EventHandlingResultIgnored
		}
		//nolint:staticcheck // intentional framework integration coverage
		return portal.Internal().HandleRemoteEvent(ctx, login, remote.GetType(), remote)
	}
	return f
}

func (f *readReceiptFramework) bridgeText(t *testing.T, logID int64) id.EventID {
	t.Helper()
	evt := events.TextMessage{ChatID: testChatID, LogID: logID, AuthorID: testOtherID, SentAt: 1700000000 + logID, Message: "synthetic"}
	if !f.kc.handleEvent(&fakeKakao{}, evt) {
		t.Fatalf("message %d was not bridged", logID)
	}
	row, err := f.bridge.DB.Message.GetLastPartByID(context.Background(), f.login.ID, makeMessageID(testChatID, logID))
	if err != nil || row == nil {
		t.Fatalf("bridged message %d row = %v, %v", logID, row, err)
	}
	return row.MXID
}

func TestDECUNREADFromMemberBecomesGhostReceiptOnExactOrEarlierMessage(t *testing.T) {
	f := newReadReceiptFramework(t)
	first := f.bridgeText(t, 100)
	second := f.bridgeText(t, 102)

	// 101 was never bridged: the receipt lands on the latest bridged event
	// at or before the watermark.
	if !f.kc.handleEvent(&fakeKakao{}, events.ReadStateChanged{ChatID: testChatID, UserID: testOtherID, Watermark: 101}) {
		t.Fatal("DECUNREAD at an unbridged watermark was not handled")
	}
	if !f.kc.handleEvent(&fakeKakao{}, events.ReadStateChanged{ChatID: testChatID, UserID: testOtherID, Watermark: 102}) {
		t.Fatal("DECUNREAD at a bridged watermark was not handled")
	}

	want := []readReceiptMark{{room: "!room:test", eventID: first}, {room: "!room:test", eventID: second}}
	if got := f.matrix.ghost.markedEvents(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("ghost receipts = %v, want %v", got, want)
	}
	if got := f.matrix.user.markedEvents(); len(got) != 0 {
		t.Fatalf("member DECUNREAD marked the Matrix user's room read: %v", got)
	}
}

func TestSelfDECUNREADMarksRoomReadForMatrixUser(t *testing.T) {
	f := newReadReceiptFramework(t)
	f.bridgeText(t, 100)
	latest := f.bridgeText(t, 102)

	if !f.kc.handleEvent(&fakeKakao{}, events.ReadStateChanged{ChatID: testChatID, UserID: testSelfID, Watermark: 102}) {
		t.Fatal("self DECUNREAD was not handled")
	}

	want := []readReceiptMark{{room: "!room:test", eventID: latest}}
	if got := f.matrix.user.markedEvents(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("double-puppet receipts = %v, want %v", got, want)
	}
	if got := f.matrix.ghost.markedEvents(); len(got) != 0 {
		t.Fatalf("self DECUNREAD produced ghost receipts: %v", got)
	}
}

func TestDECUNREADForUnknownChatOrUnmappedLogIsIgnored(t *testing.T) {
	f := newReadReceiptFramework(t)
	f.bridgeText(t, 100)
	queuedBefore := f.queued

	for _, notice := range []events.ReadStateChanged{
		{ChatID: testChatID + 1, UserID: testOtherID, Watermark: 100},
		{ChatID: testChatID, UserID: testOtherID, Watermark: 99},
	} {
		if !f.kc.handleEvent(&fakeKakao{}, notice) {
			t.Fatalf("ignored DECUNREAD %+v was reported unhandled", notice)
		}
	}

	if f.queued != queuedBefore {
		t.Fatalf("ignored DECUNREAD notices queued %d remote events", f.queued-queuedBefore)
	}
	if got := f.matrix.ghost.markedEvents(); len(got) != 0 {
		t.Fatalf("ignored DECUNREAD notices produced receipts: %v", got)
	}
}

type syncRequest struct {
	chatID, cur, max int64
	count            int32
}

func syncCaptureStep(requests *[]syncRequest, reply bson.D) testloco.Step {
	return func(peer *testloco.Peer) error {
		request, err := peer.Read()
		if err != nil {
			return err
		}
		if request.Header.Method != "SYNCMSG" {
			return fmt.Errorf("method = %q, want SYNCMSG", request.Header.Method)
		}
		raw := bson.Raw(request.Body)
		*requests = append(*requests, syncRequest{
			chatID: raw.Lookup("chatId").Int64(),
			cur:    raw.Lookup("cur").Int64(),
			max:    raw.Lookup("max").Int64(),
			count:  raw.Lookup("cnt").Int32(),
		})
		return peer.WriteBSON(request.Header.PacketID, "SYNCMSG", reply)
	}
}

// connectScriptedReadReceiptClient starts the connector on a real client whose
// carriage completes LOGINLIST with nothing to catch up, then runs steps.
func connectScriptedReadReceiptClient(t *testing.T, statePath string, steps ...testloco.Step) (*KakaoClient, []*testloco.Backend) {
	t.Helper()
	checkpoint, err := continuity.Open(statePath + ".continuity")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkpoint.CommitMessage(testChatID, 2); err != nil {
		t.Fatal(err)
	}
	loginReply := bson.D{{Key: "status", Value: int32(0)}, {Key: "chatDatas", Value: bson.A{bson.D{{Key: "c", Value: testChatID}, {Key: "l", Value: bson.D{{Key: "chatId", Value: testChatID}, {Key: "logId", Value: int64(2)}}}}}}, {Key: "eof", Value: true}, {Key: "lastTokenId", Value: int64(10)}, {Key: "lbk", Value: int32(1)}}
	dialers, backends := scriptedDialers(t, append([]testloco.Step{testloco.Expect("LOGINLIST", nil, loginReply)}, steps...)...)
	raw, err := client.OpenWithTestDialers(statePath, nil, dialers)
	if err != nil {
		t.Fatal(err)
	}
	observed := &observedClient{kakaoClient: raw}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return observed, nil })
	kc.Connect(context.Background())
	waitFor(t, func() bool { return observed.subscriptions() == 1 })
	return kc, backends
}

func matrixReadReceipt(logID int64) *bridgev2.MatrixReadReceipt {
	return &bridgev2.MatrixReadReceipt{
		Portal:       &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, makeUserLoginID(testSelfID))}},
		EventID:      id.EventID(fmt.Sprintf("$log-%d", logID)),
		ExactMessage: &database.Message{ID: makeMessageID(testChatID, logID), MXID: id.EventID(fmt.Sprintf("$log-%d", logID))},
	}
}

func TestMatrixReadReceiptSendsOneMarkReadAndCheckpointSuppressesRepeat(t *testing.T) {
	statePath := newIntegrationProfile(t)
	var requests []syncRequest
	kc, backends := connectScriptedReadReceiptClient(t, statePath,
		syncCaptureStep(&requests, bson.D{{Key: "status", Value: int32(0)}, {Key: "chatLogs", Value: bson.A{}}, {Key: "isOK", Value: true}}),
		testloco.HoldOpen(),
	)
	timers := captureReadAckTimers(kc)
	putBridgedLog(kc, 1, 1)
	putBridgedLog(kc, 2, 2)

	if err := kc.HandleMatrixReadReceipt(context.Background(), matrixReadReceipt(2)); err != nil {
		t.Fatal(err)
	}
	timers.fireAll()
	// The same receipt again is answered from the persisted acknowledgement;
	// holdStep fails the backend if a second SYNCMSG arrives.
	if err := kc.HandleMatrixReadReceipt(context.Background(), matrixReadReceipt(2)); err != nil {
		t.Fatal(err)
	}
	timers.fireAll()
	kc.Disconnect()
	waitBackends(t, backends)

	want := []syncRequest{{chatID: testChatID, cur: 0, max: 2, count: 2}}
	if fmt.Sprint(requests) != fmt.Sprint(want) {
		t.Fatalf("SYNCMSG requests = %+v, want %+v", requests, want)
	}
	reloaded, err := continuity.Open(statePath + ".continuity")
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.ReadWatermark(testChatID); got != 2 {
		t.Fatalf("persisted read acknowledgement = %d, want 2", got)
	}
}

// putBridgedLog stores one bridged Kakao log of the test chat, sent at the
// given second, as the framework would after delivering it to Matrix.
func putBridgedLog(kc *KakaoClient, logID int64, second int64, parts ...networkid.PartID) {
	if len(parts) == 0 {
		parts = []networkid.PartID{""}
	}
	for _, part := range parts {
		kc.store.(*memStore).putMessage(&database.Message{
			ID:        makeMessageID(testChatID, logID),
			PartID:    part,
			MXID:      id.EventID(fmt.Sprintf("$log-%d-%s", logID, part)),
			Room:      makePortalKey(testChatID, kc.login.ID),
			Timestamp: time.Unix(1_800_000_000+second, 0),
		})
	}
}

func TestMatrixReadReceiptDeclaresBridgedLogsAfterLastAcknowledgement(t *testing.T) {
	statePath := newIntegrationProfile(t)
	checkpoint, err := continuity.Open(statePath + ".continuity")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkpoint.CommitReadWatermark(testChatID, 10); err != nil {
		t.Fatal(err)
	}
	var requests []syncRequest
	kc, backends := connectScriptedReadReceiptClient(t, statePath,
		syncCaptureStep(&requests, bson.D{{Key: "status", Value: int32(0)}, {Key: "chatLogs", Value: bson.A{}}, {Key: "isOK", Value: true}}),
		testloco.HoldOpen(),
	)
	timers := captureReadAckTimers(kc)
	// Logs 9 and 10 are at or before the acknowledged position, 12 has two
	// parts but is one log, 11 and 12 share a second, and 15 lies after the
	// receipt. Another chat's log in the same portal never counts.
	putBridgedLog(kc, 9, 1)
	putBridgedLog(kc, 10, 2)
	putBridgedLog(kc, 11, 3)
	putBridgedLog(kc, 12, 3, "a", "b")
	putBridgedLog(kc, 14, 4)
	putBridgedLog(kc, 15, 5)
	kc.store.(*memStore).putMessage(&database.Message{
		ID:        makeMessageID(testChatID+1, 13),
		MXID:      "$foreign:test",
		Room:      makePortalKey(testChatID, kc.login.ID),
		Timestamp: time.Unix(1_800_000_004, 0),
	})

	if err := kc.HandleMatrixReadReceipt(context.Background(), matrixReadReceipt(14)); err != nil {
		t.Fatal(err)
	}
	timers.fireAll()
	kc.Disconnect()
	waitBackends(t, backends)

	want := []syncRequest{{chatID: testChatID, cur: 10, max: 14, count: 3}}
	if fmt.Sprint(requests) != fmt.Sprint(want) {
		t.Fatalf("SYNCMSG requests = %+v, want %+v", requests, want)
	}
	reloaded, err := continuity.Open(statePath + ".continuity")
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.ReadWatermark(testChatID); got != 14 {
		t.Fatalf("persisted read acknowledgement = %d, want 14", got)
	}
}

func TestMatrixReadReceiptFailureIsSurfacedRedactedWithoutRetry(t *testing.T) {
	statePath := newIntegrationProfile(t)
	var requests []syncRequest
	kc, backends := connectScriptedReadReceiptClient(t, statePath,
		syncCaptureStep(&requests, bson.D{{Key: "status", Value: int32(-500)}}),
		testloco.HoldOpen(),
	)

	timers := captureReadAckTimers(kc)
	var logged bytes.Buffer
	kc.login.Log = zerolog.New(&logged)

	// The acknowledgement is sent after the coalescing window, so its failure
	// is logged, not returned to the framework.
	if err := kc.HandleMatrixReadReceipt(context.Background(), matrixReadReceipt(2)); err != nil {
		t.Fatalf("receipt error = %v, want nil", err)
	}
	timers.fireAll()
	if !strings.Contains(logged.String(), errReadReceiptMutation.Error()) {
		t.Fatalf("failure log = %q, want the read acknowledgement category", logged.String())
	}
	if strings.Contains(logged.String(), "test-access") || strings.Contains(logged.String(), "carriage.invalid") {
		t.Fatalf("failure log leaks backend detail: %q", logged.String())
	}
	kc.Disconnect()
	waitBackends(t, backends)

	if len(requests) != 1 {
		t.Fatalf("SYNCMSG requests after failure = %d, want exactly 1", len(requests))
	}
	reloaded, err := continuity.Open(statePath + ".continuity")
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.ReadWatermark(testChatID); got != 0 {
		t.Fatalf("failed receipt persisted acknowledgement %d", got)
	}
}

func TestMatrixReadReceiptForOtherChatOrNonMessageIsIgnored(t *testing.T) {
	fake := &fakeKakao{}
	kc := connectedClient(t, fake)
	foreign := matrixReadReceipt(2)
	foreign.ExactMessage.ID = makeMessageID(testChatID+1, 2)
	nonMessage := matrixReadReceipt(2)
	nonMessage.ExactMessage = nil

	for _, receipt := range []*bridgev2.MatrixReadReceipt{foreign, nonMessage} {
		if err := kc.HandleMatrixReadReceipt(context.Background(), receipt); err != nil {
			t.Fatalf("ignored receipt returned %v", err)
		}
	}
	if got := fake.markReads(); len(got) != 0 {
		t.Fatalf("ignored receipts sent MarkRead %v", got)
	}
}

// readAckTimers captures the coalescing timers HandleMatrixReadReceipt arms so
// a test fires them instead of waiting out the window.
type readAckTimers struct {
	mu      sync.Mutex
	delays  []time.Duration
	pending []*readAckTestTimer
}

type readAckTestTimer struct {
	fire    func()
	stopped bool
	fired   bool
}

func (t *readAckTestTimer) Stop() bool {
	was := !t.stopped && !t.fired
	t.stopped = true
	return was
}

func captureReadAckTimers(kc *KakaoClient) *readAckTimers {
	timers := &readAckTimers{}
	kc.afterFunc = func(delay time.Duration, fire func()) stopper {
		timers.mu.Lock()
		defer timers.mu.Unlock()
		timer := &readAckTestTimer{fire: fire}
		timers.delays = append(timers.delays, delay)
		timers.pending = append(timers.pending, timer)
		return timer
	}
	return timers
}

func (t *readAckTimers) armed() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pending)
}

func (t *readAckTimers) fireAll() {
	t.mu.Lock()
	pending := t.pending
	t.pending = nil
	t.mu.Unlock()
	for _, timer := range pending {
		if !timer.stopped && !timer.fired {
			timer.fired = true
			timer.fire()
		}
	}
}

func TestMatrixReadReceiptsCoalescePerChat(t *testing.T) {
	fake := &fakeKakao{}
	kc := connectedClient(t, fake)
	timers := captureReadAckTimers(kc)
	other := matrixReadReceipt(4)
	other.Portal = &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID+1, makeUserLoginID(testSelfID))}}
	other.ExactMessage.ID = makeMessageID(testChatID+1, 4)

	// An active Matrix client sends a receipt per incoming message.
	for _, receipt := range []*bridgev2.MatrixReadReceipt{matrixReadReceipt(5), matrixReadReceipt(7), other, matrixReadReceipt(6)} {
		if err := kc.HandleMatrixReadReceipt(context.Background(), receipt); err != nil {
			t.Fatal(err)
		}
	}
	if got := fake.markReads(); len(got) != 0 {
		t.Fatalf("MarkRead before the coalescing window = %v, want none", got)
	}
	if got := timers.armed(); got != 2 {
		t.Fatalf("armed timers = %d, want one per chat", got)
	}
	for _, delay := range timers.delays {
		if delay != readReceiptCoalesceWindow {
			t.Fatalf("timer delay = %v, want %v", delay, readReceiptCoalesceWindow)
		}
	}
	timers.fireAll()
	got := fake.markReads()
	sort.Slice(got, func(i, j int) bool { return got[i][0] < got[j][0] })
	want := [][2]int64{{testChatID, 7}, {testChatID + 1, 4}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("MarkRead = %v, want the latest position per chat %v", got, want)
	}

	// A receipt after the window starts a new one.
	if err := kc.HandleMatrixReadReceipt(context.Background(), matrixReadReceipt(9)); err != nil {
		t.Fatal(err)
	}
	if got := timers.armed(); got != 1 {
		t.Fatalf("armed timers after the window = %d, want 1", got)
	}
	timers.fireAll()
	if got := fake.markReads(); len(got) != 3 || got[2] != [2]int64{testChatID, 9} {
		t.Fatalf("MarkRead = %v, want a third acknowledgement at 9", got)
	}
}

func TestDisconnectFlushesPendingReadAcknowledgement(t *testing.T) {
	fake := &fakeKakao{}
	kc := connectedClient(t, fake)
	timers := captureReadAckTimers(kc)
	if err := kc.HandleMatrixReadReceipt(context.Background(), matrixReadReceipt(5)); err != nil {
		t.Fatal(err)
	}
	kc.Disconnect()
	if got := fake.markReads(); fmt.Sprint(got) != fmt.Sprint([][2]int64{{testChatID, 5}}) {
		t.Fatalf("MarkRead after Disconnect = %v, want the pending acknowledgement", got)
	}
	// The stopped timer must not send it again.
	timers.fireAll()
	if got := fake.markReads(); len(got) != 1 {
		t.Fatalf("MarkRead after stopped timer = %v, want one", got)
	}
}

func TestHeldMessagesCountsStoredLogsFromTheFrameworkDatabase(t *testing.T) {
	kc, _, br := newFrameworkTestClient(t, nil)
	ctx := context.Background()
	key := makePortalKey(testChatID, kc.login.ID)
	insert := func(logID, second int64, part networkid.PartID) {
		t.Helper()
		if err := br.DB.Message.Insert(ctx, &database.Message{
			BridgeID: br.ID, ID: makeMessageID(testChatID, logID), PartID: part,
			MXID: id.EventID(fmt.Sprintf("$log-%d-%s", logID, part)), Room: key,
			SenderID: makeUserID(testOtherID), Timestamp: time.Unix(1_800_000_000+second, 0),
			Metadata: &KakaoMessageMetadata{},
		}); err != nil {
			t.Fatal(err)
		}
	}
	insert(10, 1, "")
	insert(11, 1, "")
	insert(12, 2, "a")
	insert(12, 2, "b")
	insert(14, 3, "")
	insert(15, 4, "")
	held := kc.heldMessages(testChatID)

	for _, test := range []struct {
		name        string
		after, upTo int64
		limit       int
		wantMax     int64
		wantCount   int
	}{
		{name: "after an acknowledged log sharing its second", after: 10, upTo: 14, limit: 300, wantMax: 14, wantCount: 3},
		{name: "never acknowledged", after: 0, upTo: 14, limit: 300, wantMax: 14, wantCount: 4},
		{name: "after a log that was never bridged", after: 13, upTo: 15, limit: 300, wantMax: 15, wantCount: 2},
		{name: "capped to the oldest held logs", after: 0, upTo: 15, limit: 2, wantMax: 11, wantCount: 2},
		{name: "receipt target never bridged", after: 10, upTo: 13, limit: 300, wantMax: 0, wantCount: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			gotMax, gotCount, err := held(ctx, test.after, test.upTo, test.limit)
			if err != nil {
				t.Fatal(err)
			}
			if gotMax != test.wantMax || gotCount != test.wantCount {
				t.Fatalf("held(%d, %d] = max %d count %d, want max %d count %d", test.after, test.upTo, gotMax, gotCount, test.wantMax, test.wantCount)
			}
		})
	}
}
