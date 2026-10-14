package connector

// Persistence in connector tests comes from one of two places:
//
//   - memStore (memstore_test.go) for unit tests of connector logic that only
//     reads and writes portals, messages, reactions and key-value state through
//     the store interface. It is fast and needs no framework.
//   - newFrameworkBridge (this file) when a test needs the real bridgev2
//     framework: Portal.Save, UserLogin.Save, remote-event handlers, Matrix
//     room state, database metadata decoding, or a close-and-reopen restart.
//     Reopening the same frameworkOpts.path simulates a bridge restart.
//
// recordingIntent is the Matrix side of a framework bridge: it records what
// the framework sends and lets a test decide the outcome of each call.

import (
	"context"
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
	"maunium.net/go/mautrix/bridgev2/bridgeconfig"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// frameworkOpts configures newFrameworkBridge. The zero value is a fresh
// database with one recordingIntent acting for every Matrix identity.
type frameworkOpts struct {
	// path is the SQLite file. Empty uses a new file under t.TempDir(); pass
	// the same path again to reopen the database after closing it.
	path string
	// intent acts for the bot, users and ghosts of the default Matrix
	// connector. Nil uses a new recordingIntent.
	intent *recordingIntent
	// matrix replaces the default Matrix connector; intent is then unused.
	matrix bridgev2.MatrixConnector
	// network replaces the minimal network connector, which supplies the real
	// connector's database metadata types.
	network  bridgev2.NetworkConnector
	config   *bridgeconfig.BridgeConfig
	commands func(*bridgev2.Bridge) bridgev2.CommandProcessor
}

// newFrameworkBridge returns a bridgev2.Bridge over an upgraded SQLite
// database seeded with the ghost testOtherID and the portal testChatID (room
// !room:test) received by testSelfID. The database closes when the test ends;
// a test that closes it earlier can reopen opts.path.
func newFrameworkBridge(t *testing.T, opts frameworkOpts) (*bridgev2.Bridge, *dbutil.Database) {
	t.Helper()
	ctx := context.Background()
	path := opts.path
	if path == "" {
		path = filepath.Join(t.TempDir(), "bridge.db")
	}
	raw, err := dbutil.NewWithDialect(path, "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	// Registered first so that cleanups registered later by the test, such
	// as stopping workers, run before the database closes.
	t.Cleanup(func() { _ = raw.RawDB.Close() })
	matrix := opts.matrix
	if matrix == nil {
		intent := opts.intent
		if intent == nil {
			intent = &recordingIntent{}
		}
		matrix = &frameworkMatrix{bot: intent}
	}
	network := opts.network
	if network == nil {
		network = &frameworkNetworkConnector{}
	}
	processor := opts.commands
	if processor == nil {
		processor = func(*bridgev2.Bridge) bridgev2.CommandProcessor { return nil }
	}
	bridge := bridgev2.NewBridge(networkid.BridgeID("test"), raw, zerolog.Nop(), opts.config, matrix, network, processor)
	bridge.BackgroundCtx = context.Background()
	if err := bridge.DB.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.RawDB.ExecContext(ctx, `INSERT OR IGNORE INTO ghost (bridge_id,id,name,avatar_id,avatar_hash,avatar_mxc,name_set,avatar_set,contact_info_set,is_bot,identifiers,extra_profile,metadata) VALUES ('test',?,'sender','','','',1,1,0,0,'[]',NULL,'{}')`, string(makeUserID(testOtherID))); err != nil {
		t.Fatal(err)
	}
	portal := makePortalKey(testChatID, makeUserLoginID(testSelfID))
	if _, err := raw.RawDB.ExecContext(ctx, `INSERT OR IGNORE INTO portal (bridge_id,id,receiver,mxid,parent_id,parent_receiver,relay_bridge_id,relay_login_id,other_user_id,name,topic,avatar_id,avatar_hash,avatar_mxc,name_set,avatar_set,topic_set,name_is_custom,in_space,message_request,room_type,disappear_type,disappear_timer,cap_state,metadata) VALUES ('test',?,?,'!room:test',NULL,'','','','','','','','','',0,0,0,0,0,0,'',NULL,NULL,NULL,'{}')`, string(portal.ID), string(portal.Receiver)); err != nil {
		t.Fatal(err)
	}
	return bridge, raw
}

// frameworkMatrix is the default Matrix connector. user and ghost fall back
// to bot when nil.
type frameworkMatrix struct {
	bridgev2.MatrixConnector
	bot, user, ghost *recordingIntent
}

func (m *frameworkMatrix) Init(*bridgev2.Bridge)         {}
func (m *frameworkMatrix) BotIntent() bridgev2.MatrixAPI { return m.bot }
func (m *frameworkMatrix) NewUserIntent(context.Context, id.UserID, string) (bridgev2.MatrixAPI, string, error) {
	if m.user != nil {
		return m.user, "", nil
	}
	return m.bot, "", nil
}
func (m *frameworkMatrix) GhostIntent(networkid.UserID) bridgev2.MatrixAPI {
	if m.ghost != nil {
		return m.ghost
	}
	return m.bot
}
func (m *frameworkMatrix) SendMessageStatus(context.Context, *bridgev2.MessageStatus, *bridgev2.MessageStatusEventInfo) {
}

type frameworkNetworkConnector struct{ bridgev2.NetworkConnector }

func (f *frameworkNetworkConnector) Init(*bridgev2.Bridge) {}
func (f *frameworkNetworkConnector) GetDBMetaTypes() database.MetaTypes {
	return (&KakaoConnector{}).GetDBMetaTypes()
}
func (f *frameworkNetworkConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{}
}
func (f *frameworkNetworkConnector) GetName() bridgev2.BridgeName {
	return (&KakaoConnector{}).GetName()
}

// recordedEvent is one SendMessage or SendState call.
type recordedEvent struct {
	typ     event.Type
	key     string
	content *event.Content
}

type readReceiptMark struct {
	room    id.RoomID
	eventID id.EventID
}

// recordingIntent is the Matrix intent fake for framework tests. It records
// every SendMessage and SendState attempt, accepted uploads, invites, joins
// and read markers. A hook, when set, runs after the call is recorded and
// its error fails the call; hooks run without the lock held, so they may
// block.
type recordingIntent struct {
	bridgev2.MatrixAPI
	// mxid defaults to @bot:test.
	mxid id.UserID

	onSendMessage func(ctx context.Context, typ event.Type, content *event.Content) error
	onSendState   func(typ event.Type, key string, content *event.Content) error
	onUpload      func(ctx context.Context, data []byte) error

	mu       sync.Mutex
	messages []recordedEvent
	states   []recordedEvent
	uploads  [][]byte
	invited  []id.UserID
	joined   int
	marks    []readReceiptMark
}

func (r *recordingIntent) GetMXID() id.UserID {
	if r.mxid == "" {
		return "@bot:test"
	}
	return r.mxid
}
func (r *recordingIntent) IsDoublePuppet() bool { return false }

func (r *recordingIntent) SendMessage(ctx context.Context, _ id.RoomID, typ event.Type, content *event.Content, _ *bridgev2.MatrixSendExtra) (*mautrix.RespSendEvent, error) {
	r.mu.Lock()
	r.messages = append(r.messages, recordedEvent{typ: typ, content: content})
	n := len(r.messages)
	r.mu.Unlock()
	if r.onSendMessage != nil {
		if err := r.onSendMessage(ctx, typ, content); err != nil {
			return nil, err
		}
	}
	return &mautrix.RespSendEvent{EventID: id.EventID(fmt.Sprintf("$%s-%d", r.GetMXID().Localpart(), n))}, nil
}

func (r *recordingIntent) SendState(_ context.Context, _ id.RoomID, typ event.Type, key string, content *event.Content, _ time.Time) (*mautrix.RespSendEvent, error) {
	r.mu.Lock()
	r.states = append(r.states, recordedEvent{typ: typ, key: key, content: content})
	n := len(r.states)
	r.mu.Unlock()
	if r.onSendState != nil {
		if err := r.onSendState(typ, key, content); err != nil {
			return nil, err
		}
	}
	return &mautrix.RespSendEvent{EventID: id.EventID(fmt.Sprintf("$%s-state-%d", r.GetMXID().Localpart(), n))}, nil
}

func (r *recordingIntent) UploadMedia(ctx context.Context, _ id.RoomID, data []byte, _, _ string) (id.ContentURIString, *event.EncryptedFileInfo, error) {
	if r.onUpload != nil {
		if err := r.onUpload(ctx, data); err != nil {
			return "", nil, err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.uploads = append(r.uploads, append([]byte(nil), data...))
	return "mxc://example/upload", nil, nil
}

func (r *recordingIntent) EnsureInvited(_ context.Context, _ id.RoomID, user id.UserID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.invited = append(r.invited, user)
	return nil
}

func (r *recordingIntent) EnsureJoined(context.Context, id.RoomID, ...bridgev2.EnsureJoinedParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.joined++
	return nil
}

func (r *recordingIntent) MarkRead(_ context.Context, room id.RoomID, eventID id.EventID, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.marks = append(r.marks, readReceiptMark{room: room, eventID: eventID})
	return nil
}

// reset forgets everything recorded so far.
func (r *recordingIntent) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages, r.states, r.uploads, r.invited, r.joined, r.marks = nil, nil, nil, nil, 0, nil
}

func (r *recordingIntent) sentMessages() []recordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedEvent(nil), r.messages...)
}

// sentContents returns the parsed content of every message event attempt.
func (r *recordingIntent) sentContents() []*event.MessageEventContent {
	var out []*event.MessageEventContent
	for _, m := range r.sentMessages() {
		if parsed, ok := m.content.Parsed.(*event.MessageEventContent); ok {
			out = append(out, parsed)
		}
	}
	return out
}

// sentCount returns how many SendMessage attempts had type typ, or all
// attempts when typ is the zero type.
func (r *recordingIntent) sentCount(typ event.Type) int {
	count := 0
	for _, m := range r.sentMessages() {
		if typ == (event.Type{}) || m.typ == typ {
			count++
		}
	}
	return count
}

func (r *recordingIntent) sentStates(typ event.Type) []recordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []recordedEvent
	for _, s := range r.states {
		if s.typ == typ {
			out = append(out, s)
		}
	}
	return out
}

// topics returns every m.room.topic attempt.
func (r *recordingIntent) topics() []string {
	var out []string
	for _, s := range r.sentStates(event.StateTopic) {
		out = append(out, s.content.Parsed.(*event.TopicEventContent).Topic)
	}
	return out
}

// memberChanges returns every m.room.member attempt as "state_key:membership".
func (r *recordingIntent) memberChanges() []string {
	var out []string
	for _, s := range r.sentStates(event.StateMember) {
		out = append(out, s.key+":"+string(s.content.Parsed.(*event.MemberEventContent).Membership))
	}
	return out
}

func (r *recordingIntent) uploaded() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]byte(nil), r.uploads...)
}

func (r *recordingIntent) invites() []id.UserID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]id.UserID(nil), r.invited...)
}

func (r *recordingIntent) joins() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.joined
}

func (r *recordingIntent) markedEvents() []readReceiptMark {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]readReceiptMark(nil), r.marks...)
}
