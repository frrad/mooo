package connector

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
)

type frameworkPersistenceIntent struct {
	bridgev2.MatrixAPI
	err     error
	calls   int
	entered chan struct{}
	release chan struct{}
	done    chan struct{}
	once    sync.Once
}

func (f *frameworkPersistenceIntent) GetMXID() id.UserID   { return "@bot:test" }
func (f *frameworkPersistenceIntent) IsDoublePuppet() bool { return false }
func (f *frameworkPersistenceIntent) EnsureInvited(context.Context, id.RoomID, id.UserID) error {
	return nil
}
func (f *frameworkPersistenceIntent) EnsureJoined(context.Context, id.RoomID, ...bridgev2.EnsureJoinedParams) error {
	return nil
}
func (f *frameworkPersistenceIntent) SendMessage(context.Context, id.RoomID, event.Type, *event.Content, *bridgev2.MatrixSendExtra) (*mautrix.RespSendEvent, error) {
	f.calls++
	if f.entered != nil {
		close(f.entered)
		<-f.release
		if f.done != nil {
			f.once.Do(func() { close(f.done) })
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return &mautrix.RespSendEvent{EventID: id.EventID(fmt.Sprintf("$gap-%d", f.calls))}, nil
}

type frameworkPersistenceMatrixConnector struct {
	bridgev2.MatrixConnector
	intent bridgev2.MatrixAPI
}

func (f *frameworkPersistenceMatrixConnector) Init(*bridgev2.Bridge)         {}
func (f *frameworkPersistenceMatrixConnector) BotIntent() bridgev2.MatrixAPI { return f.intent }
func (f *frameworkPersistenceMatrixConnector) NewUserIntent(context.Context, id.UserID, string) (bridgev2.MatrixAPI, string, error) {
	return f.intent, "", nil
}
func (f *frameworkPersistenceMatrixConnector) GhostIntent(networkid.UserID) bridgev2.MatrixAPI {
	return f.intent
}

type frameworkPersistenceNetworkConnector struct{ bridgev2.NetworkConnector }

func (f *frameworkPersistenceNetworkConnector) Init(*bridgev2.Bridge) {}
func (f *frameworkPersistenceNetworkConnector) GetDBMetaTypes() database.MetaTypes {
	return (&KakaoConnector{}).GetDBMetaTypes()
}
func (f *frameworkPersistenceNetworkConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{}
}

func TestFrameworkConversionGapFailureReplayAndRestartDedup(t *testing.T) {
	gap := events.PhotoMessage{Message: media.PhotoMessage{ChatID: testChatID, LogID: 100, AuthorID: testOtherID, SentAt: 1700000000, Attachment: media.PhotoAttachment{Size: 1, Checksum: "0000000000000000000000000000000000000000", MediaType: "image/jpeg", URL: "https://talk.kakaocdn.net/file", ExpiresAt: 1}}}
	runFrameworkConversionGapFailureReplayAndRestartDedup(t, gap, makeMessageID(testChatID, gap.Message.LogID), "expired")
}

func TestFrameworkMessageGapFailureReplayAndRestartDedup(t *testing.T) {
	gap := events.MessageGap{ChatID: testChatID, LogID: 101, AuthorID: testOtherID, SentAt: 1700000001, Type: 2}
	runFrameworkConversionGapFailureReplayAndRestartDedup(t, gap, makeMessageID(testChatID, gap.LogID), "malformed_payload")
}

func TestFrameworkInlineBlockedRemoteEventDisconnectLeavesSourceUncommitted(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "bridge.db")
	raw, err := dbutil.NewWithDialect(dbPath, "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	// Register database cleanup first: later worker cleanup runs before this.
	t.Cleanup(func() { _ = raw.RawDB.Close() })
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	var releaseOnce sync.Once
	var streamCloseOnce sync.Once
	intent := &frameworkPersistenceIntent{err: errors.New("blocked delivery released as failure"), entered: entered, release: release, done: done}
	bridge, err := newFrameworkConversionBridge(ctx, raw, intent)
	if err != nil {
		t.Fatal(err)
	}
	user, err := bridge.GetUserByMXID(ctx, id.UserID("@owner:test"))
	if err != nil {
		t.Fatal(err)
	}
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{}, ID: makeUserLoginID(testSelfID), UserMXID: user.MXID}, Bridge: bridge, User: user}
	var openMu sync.Mutex
	openCount := 0
	firstOpen := make(chan struct{})
	secondOpen := make(chan struct{})
	initialDone := make(chan struct{})
	replacementDone := make(chan struct{})
	freshDone := make(chan struct{})
	var pumpDone chan struct{}
	fake := &fakeKakao{stream: make(chan events.Result)}
	kc := newKakaoClient(login, testSelfID, func() (kakaoClient, error) {
		openMu.Lock()
		openCount++
		switch openCount {
		case 1:
			close(firstOpen)
		case 2:
			close(secondOpen)
		}
		openMu.Unlock()
		return fake, nil
	})
	kc.sendState = func(status.BridgeState) {}
	previousTimeout := terminalDisconnectTimeout
	terminalDisconnectTimeout = 50 * time.Millisecond
	// Cleanup registration is LIFO: workers first, timeout restoration second,
	// and the database cleanup registered above last.
	t.Cleanup(func() { terminalDisconnectTimeout = previousTimeout })
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		streamCloseOnce.Do(func() { close(fake.stream) })
		kc.Disconnect()
		for _, worker := range []chan struct{}{initialDone, replacementDone, freshDone, pumpDone} {
			if worker == nil {
				continue
			}
			select {
			case <-worker:
			case <-time.After(time.Second):
			}
		}
	})
	var queueResult bridgev2.EventHandlingResult
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		queueResult = login.QueueRemoteEvent(remote)
		return queueResult
	}
	evt := events.TextMessage{ChatID: testChatID, LogID: 102, AuthorID: testOtherID, SentAt: 1700000002, Message: "blocked inline delivery"}
	go func() { kc.Connect(ctx); close(initialDone) }()
	select {
	case <-firstOpen:
	case <-time.After(time.Second):
		t.Fatal("real client event pump did not open")
	}
	deadline := time.Now().Add(time.Second)
	for !kc.IsLoggedIn() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !kc.IsLoggedIn() {
		t.Fatal("real client event pump did not connect")
	}
	kc.mu.Lock()
	pumpDone = kc.done
	kc.mu.Unlock()
	if pumpDone == nil {
		t.Fatal("connected client did not expose event-pump completion")
	}
	fake.stream <- events.Result{Event: evt}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("inline framework handler did not reach blocked Matrix send")
	}
	if got := len(fake.committed()); got != 0 {
		t.Fatalf("source commits while Matrix delivery is blocked = %d, want 0", got)
	}
	started := time.Now()
	kc.Disconnect()
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("Disconnect while Matrix delivery is blocked took %v", elapsed)
	}
	kc.mu.Lock()
	cleanupOwner, activeClient := kc.cleanup, kc.client
	if cleanupOwner != fake || activeClient != nil {
		kc.mu.Unlock()
		t.Fatalf("blocked pump cleanup owner = (%v, %v), want retained fake and nil client", cleanupOwner == fake, activeClient == nil)
	}
	kc.mu.Unlock()
	// A retained owner prevents a replacement profile from opening while the
	// event pump still owns the old session.
	go func() { kc.Connect(ctx); close(replacementDone) }()
	select {
	case <-replacementDone:
	case <-time.After(time.Second):
		t.Fatal("replacement Connect attempt did not return")
	}
	openMu.Lock()
	gotOpenCount := openCount
	openMu.Unlock()
	if gotOpenCount != 1 {
		t.Fatalf("replacement opened while blocked pump retained owner: opens=%d", gotOpenCount)
	}
	// Retrying cleanup before the blocked pump exits must retain ownership too:
	// kc.done was cleared by the first Disconnect, but pumpDone remains live.
	kc.Disconnect()
	kc.mu.Lock()
	if kc.cleanup != fake {
		kc.mu.Unlock()
		t.Fatal("second Disconnect released cleanup owner while event pump was blocked")
	}
	kc.mu.Unlock()
	releaseOnce.Do(func() { close(release) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("inline framework handler did not finish before database cleanup")
	}
	streamCloseOnce.Do(func() { close(fake.stream) })
	select {
	case <-pumpDone:
	case <-time.After(time.Second):
		t.Fatal("event pump did not stop after blocked Matrix send was released")
	}
	if queueResult.Error == nil || queueResult.Success || queueResult.Queued {
		t.Fatalf("blocked framework delivery result = %+v, want failed non-queued result", queueResult)
	}
	kc.Disconnect()
	kc.mu.Lock()
	retained := kc.cleanup
	kc.mu.Unlock()
	if retained != nil {
		t.Fatalf("cleanup owner retained after event pump joined: %v", retained)
	}
	go func() { kc.Connect(ctx); close(freshDone) }()
	select {
	case <-secondOpen:
	case <-time.After(time.Second):
		t.Fatal("fresh connection was not admitted after retained pump joined")
	}
	select {
	case <-freshDone:
	case <-time.After(time.Second):
		t.Fatal("fresh Connect attempt did not return")
	}
	if got := len(fake.committed()); got != 0 {
		t.Fatalf("source commits after blocked Matrix delivery = %d, want 0", got)
	}
}

func runFrameworkConversionGapFailureReplayAndRestartDedup(t *testing.T, gap events.Event, gapID networkid.MessageID, wantGap string) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "bridge.db")
	intent := &frameworkPersistenceIntent{err: errors.New("synthetic Matrix send failure")}
	raw, err := dbutil.NewWithDialect(dbPath, "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := newFrameworkConversionBridge(ctx, raw, intent)
	if err != nil {
		_ = raw.RawDB.Close()
		t.Fatal(err)
	}
	portal, err := bridge.GetPortalByKey(ctx, makePortalKey(testChatID, makeUserLoginID(testSelfID)))
	if err != nil {
		_ = raw.RawDB.Close()
		t.Fatal(err)
	}
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{}, ID: makeUserLoginID(testSelfID)}, Bridge: bridge}
	kc := newKakaoClient(login, testSelfID, nil)
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		// PortalInternals is the framework's synchronous test seam for driving
		// the real remote-message handler without starting the event loop.
		//nolint:staticcheck // intentional framework integration coverage
		return portal.Internal().HandleRemoteEvent(ctx, login, remote.GetType(), remote)
	}
	fake := &fakeKakao{}

	if kc.handleEvent(fake, gap) {
		t.Fatal("failed Matrix notice was reported handled")
	}
	if got := len(fake.committed()); got != 0 {
		t.Fatalf("source commits after failed notice = %d, want 0", got)
	}
	if got, err := bridge.DB.Message.GetAllPartsByID(ctx, login.ID, gapID); err != nil {
		t.Fatal(err)
	} else if len(got) != 0 {
		t.Fatalf("database rows after failed notice = %d, want 0", len(got))
	}

	intent.err = nil
	if !kc.handleEvent(fake, gap) {
		t.Fatal("replayed Matrix notice was not handled")
	}
	if got := len(fake.committed()); got != 1 {
		t.Fatalf("source commits after successful replay = %d, want 1", got)
	}
	committedChatID, committedLogID, ok := events.MessagePosition(fake.committed()[0])
	wantChatID, wantLogID, wantPosition := events.MessagePosition(gap)
	if !ok || !wantPosition || committedChatID != wantChatID || committedLogID != wantLogID {
		t.Fatalf("source commit position = (%d, %d, %t), want (%d, %d)", committedChatID, committedLogID, ok, wantChatID, wantLogID)
	}
	rows, err := bridge.DB.Message.GetAllPartsByID(ctx, login.ID, gapID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("database rows after successful replay = %d, want 1", len(rows))
	}
	metadata, ok := rows[0].Metadata.(*KakaoMessageMetadata)
	if !ok || metadata.ConversionGap != wantGap {
		t.Fatalf("persisted metadata = %#v, want conversion gap", rows[0].Metadata)
	}

	if err := raw.RawDB.Close(); err != nil {
		t.Fatal(err)
	}
	raw2, err := dbutil.NewWithDialect(dbPath, "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw2.RawDB.Close() }()
	intent2 := &frameworkPersistenceIntent{}
	bridge2, err := newFrameworkConversionBridge(ctx, raw2, intent2)
	if err != nil {
		t.Fatal(err)
	}
	portal2, err := bridge2.GetPortalByKey(ctx, makePortalKey(testChatID, makeUserLoginID(testSelfID)))
	if err != nil {
		t.Fatal(err)
	}
	login2 := &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{}, ID: makeUserLoginID(testSelfID)}, Bridge: bridge2}
	kc2 := newKakaoClient(login2, testSelfID, nil)
	var restartResult bridgev2.EventHandlingResult
	kc2.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		//nolint:staticcheck // intentional framework integration coverage
		restartResult = portal2.Internal().HandleRemoteEvent(ctx, login2, remote.GetType(), remote)
		return restartResult
	}
	if !kc2.handleEvent(&fakeKakao{}, gap) {
		t.Fatalf("restart replay was not accepted as an ignored duplicate: result=%+v", restartResult)
	}
	if !restartResult.Ignored {
		t.Fatalf("restart replay result = %+v, want ignored duplicate", restartResult)
	}
	if intent2.calls != 0 {
		t.Fatalf("restart duplicate sent Matrix message %d times", intent2.calls)
	}
	rows2, err := bridge2.DB.Message.GetAllPartsByID(ctx, login2.ID, gapID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows2) != 1 {
		t.Fatalf("database rows after restart = %d, want 1", len(rows2))
	}
	metadata2, ok := rows2[0].Metadata.(*KakaoMessageMetadata)
	if !ok || metadata2.ConversionGap != wantGap {
		t.Fatalf("reloaded metadata = %#v, want conversion gap %q", rows2[0].Metadata, wantGap)
	}
}

func newFrameworkConversionBridge(ctx context.Context, raw *dbutil.Database, intent bridgev2.MatrixAPI) (*bridgev2.Bridge, error) {
	return newFrameworkBridge(ctx, raw, &frameworkPersistenceMatrixConnector{intent: intent})
}

func newFrameworkBridge(ctx context.Context, raw *dbutil.Database, matrix bridgev2.MatrixConnector) (*bridgev2.Bridge, error) {
	bridge := bridgev2.NewBridge(networkid.BridgeID("test"), raw, zerolog.Nop(), nil, matrix, &frameworkPersistenceNetworkConnector{}, func(*bridgev2.Bridge) bridgev2.CommandProcessor { return nil })
	bridge.BackgroundCtx = context.Background()
	if err := bridge.DB.Upgrade(ctx); err != nil {
		return nil, err
	}
	if _, err := raw.RawDB.ExecContext(ctx, `INSERT OR IGNORE INTO ghost (bridge_id,id,name,avatar_id,avatar_hash,avatar_mxc,name_set,avatar_set,contact_info_set,is_bot,identifiers,extra_profile,metadata) VALUES ('test','2000','sender','','','',1,1,0,0,'[]',NULL,'{}')`); err != nil {
		return nil, err
	}
	portal := makePortalKey(testChatID, makeUserLoginID(testSelfID))
	portalMXID := "!room:test"
	_, err := raw.RawDB.ExecContext(ctx, `INSERT OR IGNORE INTO portal (bridge_id,id,receiver,mxid,parent_id,parent_receiver,relay_bridge_id,relay_login_id,other_user_id,name,topic,avatar_id,avatar_hash,avatar_mxc,name_set,avatar_set,topic_set,name_is_custom,in_space,message_request,room_type,disappear_type,disappear_timer,cap_state,metadata) VALUES ('test',?,'1000',?,NULL,'','','','', '', '', '', '', '',0,0,0,0,0,0,'',NULL,NULL,NULL,'{}')`, portal.ID, portalMXID)
	if err != nil {
		return nil, fmt.Errorf("insert portal: %w", err)
	}
	return bridge, nil
}
