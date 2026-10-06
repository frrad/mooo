package connector

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/events"
	protocol "github.com/frrad/mooo/internal/protocol/loco"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
)

type readReceiptMark struct {
	room    id.RoomID
	eventID id.EventID
}

// readReceiptIntent is one Matrix identity in the framework read-receipt
// tests. It records receipts so the tests can tell a ghost's receipt from the
// double-puppeted user's.
type readReceiptIntent struct {
	bridgev2.MatrixAPI
	mxid  id.UserID
	mu    sync.Mutex
	sent  int
	marks []readReceiptMark
}

func (r *readReceiptIntent) GetMXID() id.UserID   { return r.mxid }
func (r *readReceiptIntent) IsDoublePuppet() bool { return false }
func (r *readReceiptIntent) EnsureInvited(context.Context, id.RoomID, id.UserID) error {
	return nil
}
func (r *readReceiptIntent) EnsureJoined(context.Context, id.RoomID, ...bridgev2.EnsureJoinedParams) error {
	return nil
}
func (r *readReceiptIntent) SendMessage(context.Context, id.RoomID, event.Type, *event.Content, *bridgev2.MatrixSendExtra) (*mautrix.RespSendEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent++
	return &mautrix.RespSendEvent{EventID: id.EventID(fmt.Sprintf("$%s-%d", r.mxid.Localpart(), r.sent))}, nil
}
func (r *readReceiptIntent) MarkRead(_ context.Context, room id.RoomID, eventID id.EventID, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.marks = append(r.marks, readReceiptMark{room: room, eventID: eventID})
	return nil
}
func (r *readReceiptIntent) markedEvents() []readReceiptMark {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]readReceiptMark(nil), r.marks...)
}

type readReceiptMatrixConnector struct {
	bridgev2.MatrixConnector
	bot   *readReceiptIntent
	user  *readReceiptIntent
	ghost *readReceiptIntent
}

func (m *readReceiptMatrixConnector) Init(*bridgev2.Bridge)         {}
func (m *readReceiptMatrixConnector) BotIntent() bridgev2.MatrixAPI { return m.bot }
func (m *readReceiptMatrixConnector) NewUserIntent(context.Context, id.UserID, string) (bridgev2.MatrixAPI, string, error) {
	return m.user, "", nil
}
func (m *readReceiptMatrixConnector) GhostIntent(networkid.UserID) bridgev2.MatrixAPI {
	return m.ghost
}

type readReceiptFramework struct {
	kc     *KakaoClient
	bridge *bridgev2.Bridge
	login  *bridgev2.UserLogin
	matrix *readReceiptMatrixConnector
	queued int
}

// newReadReceiptFramework drives the connector through the real bridgev2
// remote-event handlers and a real SQLite bridge database.
func newReadReceiptFramework(t *testing.T) *readReceiptFramework {
	t.Helper()
	ctx := context.Background()
	raw, err := dbutil.NewWithDialect(filepath.Join(t.TempDir(), "bridge.db"), "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.RawDB.Close() })
	matrix := &readReceiptMatrixConnector{
		bot:   &readReceiptIntent{mxid: "@bot:test"},
		user:  &readReceiptIntent{mxid: "@owner:test"},
		ghost: &readReceiptIntent{mxid: "@ghost:test"},
	}
	bridge, err := newFrameworkBridge(ctx, raw, matrix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.RawDB.ExecContext(ctx, `INSERT OR IGNORE INTO ghost (bridge_id,id,name,avatar_id,avatar_hash,avatar_mxc,name_set,avatar_set,contact_info_set,is_bot,identifiers,extra_profile,metadata) VALUES ('test','1000','self','','','',1,1,0,0,'[]',NULL,'{}')`); err != nil {
		t.Fatal(err)
	}
	user, err := bridge.GetUserByMXID(ctx, "@owner:test")
	if err != nil {
		t.Fatal(err)
	}
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: makeUserLoginID(testSelfID), UserMXID: user.MXID}, Bridge: bridge, User: user, Log: zerolog.Nop()}
	f := &readReceiptFramework{bridge: bridge, login: login, matrix: matrix}
	f.kc = newKakaoClient(login, testSelfID, nil)
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
		body, err := bson.Marshal(reply)
		if err != nil {
			return err
		}
		return peer.Write(protocol.Packet{Header: protocol.Header{PacketID: request.Header.PacketID, Method: "SYNCMSG", BodyType: protocol.BodyTypeBSON}, Body: body})
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
	dialers, backends := scriptedDialers(t, append([]testloco.Step{requestStep("LOGINLIST", loginReply)}, steps...)...)
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
		syncCaptureStep(&requests, bson.D{{Key: "status", Value: int32(0)}, {Key: "chatLogs", Value: bson.A{}}}),
		holdStep(),
	)

	if err := kc.HandleMatrixReadReceipt(context.Background(), matrixReadReceipt(2)); err != nil {
		t.Fatal(err)
	}
	// The same receipt again is answered from the persisted acknowledgement;
	// holdStep fails the backend if a second SYNCMSG arrives.
	if err := kc.HandleMatrixReadReceipt(context.Background(), matrixReadReceipt(2)); err != nil {
		t.Fatal(err)
	}
	kc.Disconnect()
	waitBackends(t, backends)

	want := []syncRequest{{chatID: testChatID, cur: 1, max: 2, count: 1}}
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

func TestMatrixReadReceiptFailureIsSurfacedRedactedWithoutRetry(t *testing.T) {
	statePath := newIntegrationProfile(t)
	var requests []syncRequest
	kc, backends := connectScriptedReadReceiptClient(t, statePath,
		syncCaptureStep(&requests, bson.D{{Key: "status", Value: int32(-500)}}),
		holdStep(),
	)

	err := kc.HandleMatrixReadReceipt(context.Background(), matrixReadReceipt(2))
	if !errors.Is(err, errReadReceiptMutation) {
		t.Fatalf("receipt error = %v, want read receipt mutation failure", err)
	}
	if strings.Contains(err.Error(), "test-access") || strings.Contains(err.Error(), "carriage.invalid") {
		t.Fatalf("receipt error leaks backend detail: %v", err)
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
