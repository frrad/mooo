package connector

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/media"
)

type frameworkPersistenceIntent struct {
	bridgev2.MatrixAPI
	err   error
	calls int
}

func (f *frameworkPersistenceIntent) GetMXID() id.UserID   { return "@bot:test" }
func (f *frameworkPersistenceIntent) IsDoublePuppet() bool { return false }
func (f *frameworkPersistenceIntent) SendMessage(context.Context, id.RoomID, event.Type, *event.Content, *bridgev2.MatrixSendExtra) (*mautrix.RespSendEvent, error) {
	f.calls++
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
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: makeUserLoginID(testSelfID)}, Bridge: bridge}
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
	login2 := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: makeUserLoginID(testSelfID)}, Bridge: bridge2}
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
	if intent2.calls != 0 {
		t.Fatalf("restart duplicate sent Matrix message %d times", intent2.calls)
	}
}

func newFrameworkConversionBridge(ctx context.Context, raw *dbutil.Database, intent bridgev2.MatrixAPI) (*bridgev2.Bridge, error) {
	matrix := &frameworkPersistenceMatrixConnector{intent: intent}
	bridge := bridgev2.NewBridge(networkid.BridgeID("test"), raw, zerolog.Nop(), nil, matrix, &frameworkPersistenceNetworkConnector{}, func(*bridgev2.Bridge) bridgev2.CommandProcessor { return nil })
	bridge.BackgroundCtx = context.Background()
	if err := bridge.DB.Upgrade(ctx); err != nil {
		return nil, err
	}
	if _, err := raw.RawDB.ExecContext(ctx, `INSERT OR IGNORE INTO ghost (bridge_id,id,name,avatar_id,avatar_hash,avatar_mxc,name_set,avatar_set,contact_info_set,is_bot,identifiers,extra_profile,metadata) VALUES ('test','2000','sender','','','',1,1,0,0,'[]',NULL,'{}')`); err != nil {
		return nil, err
	}
	portal := makePortalKey(testChatID, makeUserLoginID(testSelfID))
	_, err := raw.RawDB.ExecContext(ctx, `INSERT OR IGNORE INTO portal (bridge_id,id,receiver,mxid,parent_id,parent_receiver,relay_bridge_id,relay_login_id,other_user_id,name,topic,avatar_id,avatar_hash,avatar_mxc,name_set,avatar_set,topic_set,name_is_custom,in_space,message_request,room_type,disappear_type,disappear_timer,cap_state,metadata) VALUES ('test',?,'1000','!room:test',NULL,'','','','', '', '', '', '', '',0,0,0,0,0,0,'',NULL,NULL,NULL,'{}')`, portal.ID)
	if err != nil {
		return nil, fmt.Errorf("insert portal: %w", err)
	}
	return bridge, nil
}
