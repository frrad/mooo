package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/macweb"
	"github.com/frrad/mooo/internal/protocol/reactions"
)

type reactionTestBackend struct {
	*fakeKakao
	members     reactions.MembersResponse
	err         error
	memberCalls int
	requests    []reactions.Request
	mutationErr error
	mini        reactions.DetailsResponse
	miniErr     error
}

func (b *reactionTestBackend) MiniReactionDetails(context.Context, int64, int64, int64) (reactions.DetailsResponse, error) {
	return b.mini, b.miniErr
}

func TestObservedMiniReactionAttribution(t *testing.T) {
	backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, members: reactions.MembersResponse{Revision: 2, Members: map[reactions.Type][]int64{reactions.Heart: {1000}}}, mini: reactions.DetailsResponse{Details: []reactions.Detail{{Kind: 2, ReactionID: "synthetic_021", UserIDs: []int64{42}}}}}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return backend, nil })
	change := events.ReactionChanged{MetadataType: 2, ChatID: testChatID, LogID: 99, Revision: 9, Items: []events.ReactionItem{{Kind: 2, ID: "synthetic_021", Count: 1, Alt: map[string]string{"ko": "synthetic label"}}}}
	remote, err := kc.reactionRemote(context.Background(), backend, change)
	if err != nil {
		t.Fatal(err)
	}
	sync := remote.(*kakaoReactionSync)
	rs := sync.Reactions.Users[makeUserID(42)].Reactions
	if len(rs) != 1 || rs[0].EmojiID != "kakao:mini:synthetic_021" || rs[0].Emoji != "synthetic label" || !sync.IncludesMini {
		t.Fatalf("%#v", rs)
	}
	if len(sync.Reactions.Users[makeUserID(1000)].Reactions) != 1 {
		t.Fatal("legacy state lost")
	}
	backend.mini.Details[0].UserIDs = []int64{42, 43}
	if _, err = kc.reactionRemote(context.Background(), backend, change); err == nil {
		t.Fatal("aggregate/detail disagreement accepted")
	}
	backend.mini.Details = []reactions.Detail{}
	change.Items = nil
	remote, err = kc.reactionRemote(context.Background(), backend, change)
	if err != nil || len(remote.(*kakaoReactionSync).Reactions.Users) != 1 {
		t.Fatalf("empty mini state: %v", err)
	}
	backend.members = reactions.MembersResponse{Revision: 0, Fields: map[string]json.RawMessage{"revision": []byte(`0`)}}
	backend.mini.Details = []reactions.Detail{{Kind: 2, ReactionID: "synthetic_021", UserIDs: []int64{42}}}
	change.Items = []events.ReactionItem{{Kind: 2, ID: "synthetic_021", Count: 1}}
	if _, err := kc.reactionRemote(context.Background(), backend, change); err != nil {
		t.Fatalf("observed empty legacy roster: %v", err)
	}
	kc.reactionRevisions[reactionRevisionCacheKey(events.ReactionChanged{ChatID: change.ChatID, LogID: change.LogID})] = 12
	if _, err := kc.reactionRemote(context.Background(), backend, change); err == nil {
		t.Fatal("mini snapshot rolled back newer legacy state")
	}
}

func TestMiniRevisionsAndRemovalScope(t *testing.T) {
	ctx := context.Background()
	kc, _, login, old, raw := newFrameworkReactionFixture(t, &frameworkReactionIntent{})
	defer func() { _ = raw.RawDB.Close() }()
	legacy := events.ReactionChanged{ChatID: testChatID, LogID: 99, Revision: 100}
	mini := events.ReactionChanged{MetadataType: 2, ChatID: testChatID, LogID: 99, Revision: 2}
	if err := kc.persistReactionRevision(ctx, legacy, 100); err != nil {
		t.Fatal(err)
	}
	if err := kc.persistReactionRevision(ctx, mini, 2); err != nil {
		t.Fatal(err)
	}
	reopened := newKakaoClient(login, testSelfID, nil)
	for _, tc := range []struct {
		change events.ReactionChanged
		want   int64
	}{{legacy, 100}, {mini, 2}} {
		got, err := reopened.storedReactionRevision(ctx, tc.change)
		if err != nil || got != tc.want {
			t.Fatalf("revision=%d err=%v", got, err)
		}
	}
	old.EmojiID = "kakao:mini:synthetic"
	old.MXID = "$mini"
	if err := login.Bridge.DB.Reaction.Upsert(ctx, old); err != nil {
		t.Fatal(err)
	}
	sync := &kakaoReactionSync{ReactionSync: simplevent.ReactionSync{EventMeta: simplevent.EventMeta{PortalKey: old.Room}, TargetMessage: old.MessageID, Reactions: &bridgev2.ReactionSyncData{Users: map[networkid.UserID]*bridgev2.ReactionSyncUser{}, HasAllUsers: true}}}
	planned, err := kc.reactionDeliveryEvents(ctx, sync)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range planned {
		if r.(*simplevent.Reaction).EmojiID == old.EmojiID {
			t.Fatal("legacy-only update removed mini reaction")
		}
	}
	sync.IncludesMini = true
	planned, err = kc.reactionDeliveryEvents(ctx, sync)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range planned {
		if r.(*simplevent.Reaction).EmojiID == old.EmojiID && r.GetType() == bridgev2.RemoteEventReactionRemove {
			found = true
		}
	}
	if !found {
		t.Fatal("empty confirmed mini roster did not remove mini reaction")
	}
}

type frameworkReactionIntent struct {
	bridgev2.MatrixAPI
	err      error
	calls    int
	types    []event.Type
	failType event.Type
}

type frameworkMatrixConnector struct {
	bridgev2.MatrixConnector
	intent bridgev2.MatrixAPI
}

func (f *frameworkMatrixConnector) Init(*bridgev2.Bridge)                           {}
func (f *frameworkMatrixConnector) BotIntent() bridgev2.MatrixAPI                   { return f.intent }
func (f *frameworkMatrixConnector) GhostIntent(networkid.UserID) bridgev2.MatrixAPI { return f.intent }
func (f *frameworkMatrixConnector) SendMessageStatus(context.Context, *bridgev2.MessageStatus, *bridgev2.MessageStatusEventInfo) {
}

type frameworkNetworkConnector struct {
	bridgev2.NetworkConnector
}

func (f *frameworkNetworkConnector) Init(*bridgev2.Bridge) {}
func (f *frameworkNetworkConnector) GetDBMetaTypes() database.MetaTypes {
	return (&KakaoConnector{}).GetDBMetaTypes()
}
func (f *frameworkNetworkConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{}
}

func (f *frameworkReactionIntent) GetMXID() id.UserID { return "@bot:test" }
func (f *frameworkReactionIntent) SendMessage(_ context.Context, _ id.RoomID, kind event.Type, _ *event.Content, _ *bridgev2.MatrixSendExtra) (*mautrix.RespSendEvent, error) {
	return f.sendMessage(kind, nil)
}

func (f *frameworkReactionIntent) sendMessage(kind event.Type, err error) (*mautrix.RespSendEvent, error) {
	f.calls++
	f.types = append(f.types, kind)
	if err != nil || (f.err != nil && (f.failType == (event.Type{}) || f.failType == kind)) {
		if f.err != nil {
			return nil, f.err
		}
		return nil, err
	}
	return &mautrix.RespSendEvent{EventID: id.EventID(fmt.Sprintf("$reaction-%d", f.calls))}, nil
}

func countFrameworkEvents(types []event.Type, want event.Type) int {
	count := 0
	for _, typ := range types {
		if typ == want {
			count++
		}
	}
	return count
}

func (b *reactionTestBackend) React(_ context.Context, request reactions.Request) (reactions.Response, error) {
	b.requests = append(b.requests, request)
	if b.mutationErr != nil {
		return reactions.Response{}, b.mutationErr
	}
	return reactions.Response{Status: 0}, nil
}

func (b *reactionTestBackend) ReactionMembers(context.Context, int64, int64) (reactions.MembersResponse, error) {
	b.memberCalls++
	return b.members, b.err
}

func TestReactionSyncUsersRequiresCompletePositiveLegacyRevision(t *testing.T) {
	users, err := reactionSyncUsers(reactions.MembersResponse{
		Revision: 7,
		Members:  map[reactions.Type][]int64{reactions.Heart: {42, 42}, reactions.Like: {43}},
		Fields: map[string]json.RawMessage{
			"1":        []byte(`[42,42]`),
			"2":        []byte(`[43]`),
			"revision": []byte(`7`),
		},
	}, 1000, "1000")
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || !users[makeUserID(42)].HasAllReactions || len(users[makeUserID(42)].Reactions) != 1 {
		t.Fatalf("users = %#v", users)
	}
	if _, err := reactionSyncUsers(reactions.MembersResponse{Revision: 0}, 1000, "1000"); err == nil {
		t.Fatal("missing revision accepted")
	}
}

func TestReactionSyncUsersRejectsConflictingAndUnknownBuckets(t *testing.T) {
	base := reactions.MembersResponse{
		Revision: 2,
		Members:  map[reactions.Type][]int64{reactions.Heart: {42}, reactions.Like: {42}},
		Fields:   map[string]json.RawMessage{"1": []byte(`[42]`), "2": []byte(`[42]`)},
	}
	if _, err := reactionSyncUsers(base, 1000, "1000"); err == nil {
		t.Fatal("conflicting attribution accepted")
	}
	base.Members = nil
	base.Fields = map[string]json.RawMessage{"7": []byte(`[42]`)}
	if _, err := reactionSyncUsers(base, 1000, "1000"); err == nil {
		t.Fatal("unknown numeric bucket accepted")
	}
}

func TestReactionDeliveryEventsUsesIndividualOperationsAndPreservesUnknownRows(t *testing.T) {
	ctx := context.Background()
	raw, err := dbutil.NewWithDialect(":memory:", "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.RawDB.Close() }()
	db := database.New("test", (&KakaoConnector{}).GetDBMetaTypes(), raw)
	if err := db.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.RawDB.ExecContext(ctx, `INSERT INTO ghost (bridge_id,id,name,avatar_id,avatar_hash,avatar_mxc,name_set,avatar_set,contact_info_set,is_bot,identifiers,extra_profile,metadata) VALUES ('test','2000','sender','','','',1,1,0,0,'[]',NULL,'{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.RawDB.ExecContext(ctx, `INSERT INTO portal (bridge_id,id,receiver,mxid,parent_id,parent_receiver,relay_bridge_id,relay_login_id,other_user_id,name,topic,avatar_id,avatar_hash,avatar_mxc,name_set,avatar_set,topic_set,name_is_custom,in_space,message_request,room_type,disappear_type,disappear_timer,cap_state,metadata) VALUES ('test','3000','1000',NULL,NULL,'','','','', '', '', '', '', '',0,0,0,0,0,0,'',NULL,NULL,NULL,'{}')`); err != nil {
		t.Fatal(err)
	}
	message := &database.Message{BridgeID: "test", ID: makeMessageID(testChatID, 99), PartID: "0", MXID: "$event", Room: makePortalKey(testChatID, makeUserLoginID(testSelfID)), SenderID: makeUserID(testOtherID), Timestamp: time.Now(), Metadata: newKakaoMessageMetadata(testChatID, 99, testOtherID, 1, "", 0)}
	if err := db.Message.Insert(ctx, message); err != nil {
		t.Fatal(err)
	}
	known := &database.Reaction{Room: message.Room, MessageID: message.ID, MessagePartID: message.PartID, SenderID: makeUserID(testOtherID), EmojiID: "kakao:legacy:1", MXID: "$known", Timestamp: time.Now()}
	unknown := &database.Reaction{Room: message.Room, MessageID: message.ID, MessagePartID: message.PartID, SenderID: makeUserID(2001), EmojiID: "unknown", MXID: "$unknown", Timestamp: time.Now()}
	if err := db.Reaction.Upsert(ctx, known); err != nil {
		t.Fatal(err)
	}
	if err := db.Reaction.Upsert(ctx, unknown); err != nil {
		t.Fatal(err)
	}
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{}, ID: makeUserLoginID(testSelfID)}, Bridge: &bridgev2.Bridge{DB: db}}
	kc := newKakaoClient(login, testSelfID, nil)
	sync := &kakaoReactionSync{ReactionSync: simplevent.ReactionSync{EventMeta: simplevent.EventMeta{PortalKey: message.Room}, TargetMessage: message.ID, Reactions: &bridgev2.ReactionSyncData{Users: map[networkid.UserID]*bridgev2.ReactionSyncUser{}, HasAllUsers: true}}}
	events, err := kc.reactionDeliveryEvents(ctx, sync)
	if err != nil || len(events) != 1 {
		t.Fatalf("planned events = %#v, err=%v", events, err)
	}
	remove, ok := events[0].(*simplevent.Reaction)
	if !ok || remove.Type != bridgev2.RemoteEventReactionRemove || remove.EmojiID != known.EmojiID {
		t.Fatalf("planned removal = %#v", events[0])
	}
}

func newFrameworkReactionFixture(t *testing.T, intent *frameworkReactionIntent) (*KakaoClient, *bridgev2.Portal, *bridgev2.UserLogin, *database.Reaction, *dbutil.Database) {
	t.Helper()
	ctx := context.Background()
	raw, err := dbutil.NewWithDialect(":memory:", "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	matrix := &frameworkMatrixConnector{intent: intent}
	bridge := bridgev2.NewBridge("test", raw, zerolog.Nop(), nil, matrix, &frameworkNetworkConnector{}, func(*bridgev2.Bridge) bridgev2.CommandProcessor { return nil })
	db := bridge.DB
	if err := db.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.RawDB.ExecContext(ctx, `INSERT INTO ghost (bridge_id,id,name,avatar_id,avatar_hash,avatar_mxc,name_set,avatar_set,contact_info_set,is_bot,identifiers,extra_profile,metadata) VALUES ('test','2000','sender','','','',1,1,0,0,'[]',NULL,'{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.RawDB.ExecContext(ctx, `INSERT INTO portal (bridge_id,id,receiver,mxid,parent_id,parent_receiver,relay_bridge_id,relay_login_id,other_user_id,name,topic,avatar_id,avatar_hash,avatar_mxc,name_set,avatar_set,topic_set,name_is_custom,in_space,message_request,room_type,disappear_type,disappear_timer,cap_state,metadata) VALUES ('test','3000','1000',NULL,NULL,'','','','', '', '', '', '', '',0,0,0,0,0,0,'',NULL,NULL,NULL,'{}')`); err != nil {
		t.Fatal(err)
	}
	key := makePortalKey(testChatID, makeUserLoginID(testSelfID))
	message := &database.Message{BridgeID: "test", ID: makeMessageID(testChatID, 99), PartID: "0", MXID: "$event", Room: key, SenderID: makeUserID(testOtherID), SenderMXID: "@sender:test", Timestamp: time.Now(), Metadata: newKakaoMessageMetadata(testChatID, 99, testOtherID, 1, "", 0)}
	if err := db.Message.Insert(ctx, message); err != nil {
		t.Fatal(err)
	}
	old := &database.Reaction{Room: key, MessageID: message.ID, MessagePartID: message.PartID, SenderID: makeUserID(testOtherID), SenderMXID: intent.GetMXID(), EmojiID: "kakao:legacy:1", MXID: "$old", Timestamp: time.Now()}
	if err := db.Reaction.Upsert(ctx, old); err != nil {
		t.Fatal(err)
	}
	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: key, MXID: "!room:test"}, Bridge: bridge}
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{}, ID: key.Receiver}, Bridge: bridge}
	kc := newKakaoClient(login, testSelfID, nil)
	return kc, portal, login, old, raw
}

func TestReactionHandleUsesFrameworkOperationsAndPersistsOnlyAfterAllACK(t *testing.T) {
	ctx := context.Background()
	intent := &frameworkReactionIntent{err: errors.New("synthetic redaction failure"), failType: event.EventRedaction}
	kc, portal, login, old, raw := newFrameworkReactionFixture(t, intent)
	defer func() { _ = raw.RawDB.Close() }()
	backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, members: reactions.MembersResponse{Revision: 2, Members: map[reactions.Type][]int64{reactions.Like: {testOtherID}}, Fields: map[string]json.RawMessage{"2": []byte(`[2000]`)}}}
	kc.client = backend
	login.Client = kc
	ghost, ghostErr := portal.Bridge.GetGhostByID(ctx, makeUserID(testOtherID))
	if ghostErr != nil || ghost == nil {
		t.Fatalf("ghost lookup failed: ghost=%#v err=%v", ghost, ghostErr)
	}
	kc.queue = func(evt bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		return (*bridgev2.PortalInternals)(portal).HandleRemoteEvent(ctx, login, evt.GetType(), evt)
	}
	change := events.ReactionChanged{ChatID: testChatID, LogID: 99, Revision: 1}
	if kc.handleEvent(backend, change) {
		t.Fatal("redaction failure reported success")
	}
	if got := countFrameworkEvents(intent.types, event.EventReaction); got != 1 {
		t.Fatalf("initial addition attempts = %d, want 1", got)
	}
	if got := countFrameworkEvents(intent.types, event.EventRedaction); got != 1 {
		t.Fatalf("initial removal attempts = %d, want 1", got)
	}
	row, err := kc.login.Bridge.DB.Reaction.GetByIDWithoutMessagePart(ctx, kc.login.ID, old.MessageID, old.SenderID, old.EmojiID)
	if err != nil || row == nil {
		t.Fatalf("failed removal changed database row: row=%#v err=%v", row, err)
	}
	msg, err := kc.login.Bridge.DB.Message.GetFirstPartByID(ctx, kc.login.ID, old.MessageID)
	if err != nil || msg.Metadata.(*KakaoMessageMetadata).ReactionRevision != 0 {
		t.Fatalf("failed removal advanced revision: msg=%#v err=%v", msg, err)
	}
	intent.err = nil
	if !kc.handleEvent(backend, change) {
		t.Fatal("successful replay was rejected")
	}
	if got := countFrameworkEvents(intent.types, event.EventReaction); got != 1 {
		t.Fatalf("replay resent successful addition %d times", got)
	}
	if got := countFrameworkEvents(intent.types, event.EventRedaction); got != 2 {
		t.Fatalf("replay removal attempts = %d, want 2", got)
	}
	row, err = kc.login.Bridge.DB.Reaction.GetByIDWithoutMessagePart(ctx, kc.login.ID, old.MessageID, old.SenderID, old.EmojiID)
	if err != nil || row != nil {
		t.Fatalf("successful removal retained row: row=%#v err=%v", row, err)
	}
	msg, err = kc.login.Bridge.DB.Message.GetFirstPartByID(ctx, kc.login.ID, old.MessageID)
	revision := int64(-1)
	switch metadata := msg.Metadata.(type) {
	case *KakaoMessageMetadata:
		revision = metadata.ReactionRevision
	case KakaoMessageMetadata:
		revision = metadata.ReactionRevision
	}
	if err != nil || revision != 2 {
		t.Fatalf("successful removal did not persist revision: msg=%#v err=%v", msg, err)
	}
}

func TestReactionNoOpReconciliationPersistsRevision(t *testing.T) {
	ctx := context.Background()
	intent := &frameworkReactionIntent{}
	kc, portal, login, old, raw := newFrameworkReactionFixture(t, intent)
	defer func() { _ = raw.RawDB.Close() }()
	backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, members: reactions.MembersResponse{
		Revision: 2,
		Members:  map[reactions.Type][]int64{reactions.Heart: {testOtherID}},
		Fields:   map[string]json.RawMessage{"1": []byte(`[2000]`)},
	}}
	kc.client = backend
	kc.queue = func(evt bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		return (*bridgev2.PortalInternals)(portal).HandleRemoteEvent(ctx, login, evt.GetType(), evt)
	}
	if !kc.handleEvent(backend, events.ReactionChanged{ChatID: testChatID, LogID: 99, Revision: 1}) {
		t.Fatal("authoritative no-op was not acknowledged")
	}
	msg, err := kc.login.Bridge.DB.Message.GetFirstPartByID(ctx, kc.login.ID, old.MessageID)
	revision := int64(-1)
	switch metadata := msg.Metadata.(type) {
	case *KakaoMessageMetadata:
		revision = metadata.ReactionRevision
	case KakaoMessageMetadata:
		revision = metadata.ReactionRevision
	}
	if err != nil || revision != 2 {
		t.Fatalf("no-op did not persist revision: msg=%#v err=%v", msg, err)
	}
}

func TestReactionRemovalRequiresDatabaseDeletionPostcondition(t *testing.T) {
	ctx := context.Background()
	intent := &frameworkReactionIntent{}
	kc, portal, login, old, raw := newFrameworkReactionFixture(t, intent)
	defer func() { _ = raw.RawDB.Close() }()
	if _, err := raw.RawDB.ExecContext(ctx, `CREATE TRIGGER reaction_delete_fail BEFORE DELETE ON reaction BEGIN SELECT RAISE(FAIL, 'synthetic delete failure'); END`); err != nil {
		t.Fatal(err)
	}
	backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, members: reactions.MembersResponse{Revision: 2, Fields: map[string]json.RawMessage{}}}
	kc.client = backend
	kc.queue = func(evt bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		return (*bridgev2.PortalInternals)(portal).HandleRemoteEvent(ctx, login, evt.GetType(), evt)
	}
	if kc.handleEvent(backend, events.ReactionChanged{ChatID: testChatID, LogID: 99, Revision: 1}) {
		t.Fatal("stale database row was reported as successfully removed")
	}
	msg, err := kc.login.Bridge.DB.Message.GetFirstPartByID(ctx, kc.login.ID, old.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	revision := int64(-1)
	switch metadata := msg.Metadata.(type) {
	case *KakaoMessageMetadata:
		revision = metadata.ReactionRevision
	case KakaoMessageMetadata:
		revision = metadata.ReactionRevision
	}
	if revision != 0 {
		t.Fatalf("failed database deletion advanced revision to %d", revision)
	}
	if _, err := raw.RawDB.ExecContext(ctx, `DROP TRIGGER reaction_delete_fail`); err != nil {
		t.Fatal(err)
	}
	if !kc.handleEvent(backend, events.ReactionChanged{ChatID: testChatID, LogID: 99, Revision: 1}) {
		t.Fatal("replay after database recovery failed")
	}
	row, err := kc.login.Bridge.DB.Reaction.GetByIDWithoutMessagePart(ctx, kc.login.ID, old.MessageID, old.SenderID, old.EmojiID)
	if err != nil || row != nil {
		t.Fatalf("recovered removal retained row: row=%#v err=%v", row, err)
	}
	msg, err = kc.login.Bridge.DB.Message.GetFirstPartByID(ctx, kc.login.ID, old.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	revision = -1
	switch metadata := msg.Metadata.(type) {
	case *KakaoMessageMetadata:
		revision = metadata.ReactionRevision
	case KakaoMessageMetadata:
		revision = metadata.ReactionRevision
	}
	if revision != 2 {
		t.Fatalf("recovered removal did not persist revision: %d", revision)
	}
}

func TestFrameworkOutboundReactionMaxOneReplacesAndStaleRemovalDoesNotCancel(t *testing.T) {
	ctx := context.Background()
	intent := &frameworkReactionIntent{}
	kc, portal, login, old, raw := newFrameworkReactionFixture(t, intent)
	defer func() { _ = raw.RawDB.Close() }()
	// The existing reaction belongs to the logged-in Kakao user and is the
	// reaction that MaxReactions=1 must replace.
	if err := kc.login.Bridge.DB.Reaction.Delete(ctx, old); err != nil {
		t.Fatal(err)
	}
	old.SenderID = makeUserID(testSelfID)
	old.SenderMXID = intent.GetMXID()
	if err := kc.login.Bridge.DB.Reaction.Upsert(ctx, old); err != nil {
		t.Fatal(err)
	}
	backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, members: reactions.MembersResponse{
		Revision: 2,
		Members:  map[reactions.Type][]int64{reactions.Heart: {testSelfID}},
		Fields:   map[string]json.RawMessage{"1": []byte(`[1000]`)},
	}}
	kc.client = backend
	login.Client = kc
	login.UserMXID = intent.GetMXID()
	portal.MXID = "!room:test"
	evt := &event.Event{Sender: login.UserMXID, Content: event.Content{Parsed: &event.ReactionEventContent{RelatesTo: event.RelatesTo{Type: event.RelAnnotation, EventID: "$event", Key: "👍"}}}}
	result := (*bridgev2.PortalInternals)(portal).HandleMatrixReaction(ctx, login, evt)
	if !result.Success {
		t.Fatalf("framework outbound reaction failed: %+v", result)
	}
	if len(backend.requests) != 1 || backend.requests[0].Type != reactions.Like {
		t.Fatalf("Kakao mutations = %#v, want one Like", backend.requests)
	}
	if got := countFrameworkEvents(intent.types, event.EventRedaction); got != 1 {
		t.Fatalf("Matrix old-reaction redactions = %d, want 1", got)
	}
	oldRow, err := kc.login.Bridge.DB.Reaction.GetByIDWithoutMessagePart(ctx, kc.login.ID, old.MessageID, old.SenderID, old.EmojiID)
	if err != nil || oldRow != nil {
		t.Fatalf("old reaction row was not removed: row=%#v err=%v", oldRow, err)
	}
	newRow, err := kc.login.Bridge.DB.Reaction.GetByIDWithoutMessagePart(ctx, kc.login.ID, old.MessageID, makeUserID(testSelfID), "kakao:legacy:2")
	if err != nil || newRow == nil {
		t.Fatalf("replacement reaction missing: row=%#v err=%v", newRow, err)
	}
	// A delayed redaction for the old Matrix reaction must not cancel the new
	// Kakao reaction after the remote membership lookup has changed.
	backend.members.Members = map[reactions.Type][]int64{reactions.Like: {testSelfID}}
	remove := &bridgev2.MatrixReactionRemove{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.RedactionEventContent]{Event: &event.Event{Sender: login.UserMXID}, Portal: portal},
		TargetReaction:  old,
	}
	if err := kc.HandleMatrixReactionRemove(ctx, remove); err != nil {
		t.Fatal(err)
	}
	if len(backend.requests) != 1 {
		t.Fatalf("stale old reaction caused remote cancel: %#v", backend.requests)
	}
}

func TestLegacyReactionEmojiTableIsExplicit(t *testing.T) {
	want := map[string]reactions.Type{"❤️": reactions.Heart, "❤": reactions.Heart, "👍": reactions.Like, "✅": reactions.Check, "😆": reactions.Laugh, "😮": reactions.Surprise, "😢": reactions.Sad}
	for emoji, typeID := range want {
		entry, ok := reactionForEmoji(emoji)
		if !ok || entry.typeID != typeID {
			t.Fatalf("emoji %q = %#v, %v; want type %d", emoji, entry, ok, typeID)
		}
	}
	if _, ok := reactionForEmoji("🔥"); ok {
		t.Fatal("unsupported emoji accepted")
	}
}

func TestReactionEventFetchesMembersForEmptyAggregateAndPersistsRevision(t *testing.T) {
	backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, members: reactions.MembersResponse{
		Revision: 4,
		Members:  map[reactions.Type][]int64{reactions.Heart: {testSelfID}},
		Fields:   map[string]json.RawMessage{"1": []byte(`[1000]`)},
	}}
	kc, harness := newTestClient(t, func() (kakaoClient, error) { return backend, nil })
	kc.client = backend
	change := events.ReactionChanged{ChatID: testChatID, LogID: 99, Revision: 4}
	if !kc.handleEvent(backend, change) {
		t.Fatal("reaction event was not handled")
	}
	if len(harness.queued) != 1 {
		t.Fatalf("queued = %d, want 1", len(harness.queued))
	}
	syncEvent, ok := harness.queued[0].(*kakaoReactionSync)
	if !ok || len(syncEvent.Reactions.Users) != 1 || !syncEvent.Reactions.HasAllUsers {
		t.Fatalf("queued event = %#v", harness.queued[0])
	}
	selfReaction := syncEvent.Reactions.Users[makeUserID(testSelfID)].Reactions[0]
	if !selfReaction.Sender.IsFromMe || selfReaction.Sender.SenderLogin != kc.login.ID {
		t.Fatalf("self reaction sender = %#v", selfReaction.Sender)
	}
	if !kc.handleEvent(backend, change) {
		t.Fatal("duplicate reaction event was not handled")
	}
	if len(harness.queued) != 1 {
		t.Fatalf("stale revision queued again: %d", len(harness.queued))
	}
}

func TestReactionEventRejectsMiniAndLookupFailureWithoutQueue(t *testing.T) {
	backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, members: reactions.MembersResponse{
		Revision: 4,
		Fields:   map[string]json.RawMessage{},
	}}
	kc, harness := newTestClient(t, func() (kakaoClient, error) { return backend, nil })
	kc.client = backend
	if !kc.handleEvent(backend, events.ReactionChanged{ChatID: testChatID, LogID: 99, Revision: 1, Items: []events.ReactionItem{{Kind: 2}}}) {
		t.Fatal("unsupported reaction notice was not handled")
	}
	if len(harness.queued) != 1 {
		t.Fatal("unsupported reaction notice was not queued")
	}
	backend.err = errors.New("synthetic lookup failure")
	if !kc.handleEvent(backend, events.ReactionChanged{ChatID: testChatID, LogID: 100, Revision: 1}) {
		t.Fatal("lookup failure notice was not handled")
	}
	if len(harness.queued) != 2 {
		t.Fatal("lookup failure notice was not queued")
	}
}

func TestReactionEventRejectsOlderMembersRevision(t *testing.T) {
	backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, members: reactions.MembersResponse{
		Revision: 2,
		Members:  map[reactions.Type][]int64{reactions.Heart: {42}},
		Fields:   map[string]json.RawMessage{"1": []byte(`[42]`)},
	}}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return backend, nil })
	kc.client = backend
	if _, err := kc.reactionRemote(context.Background(), backend, events.ReactionChanged{ChatID: testChatID, LogID: 99, Revision: 9}); err == nil {
		t.Fatal("older members revision accepted for newer event")
	}
}

func TestReactionRevisionAdvancesOnlyAfterSuccessfulHandling(t *testing.T) {
	backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, members: reactions.MembersResponse{
		Revision: 4, Members: map[reactions.Type][]int64{reactions.Heart: {42}}, Fields: map[string]json.RawMessage{"1": []byte(`[42]`)},
	}}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return backend, nil })
	kc.client = backend
	queued := 0
	kc.queue = func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		queued++
		if queued == 1 {
			return bridgev2.EventHandlingResultIgnored
		}
		return bridgev2.EventHandlingResultSuccess
	}
	change := events.ReactionChanged{ChatID: testChatID, LogID: 99, Revision: 4}
	if !kc.handleEvent(backend, change) {
		t.Fatal("ignored reaction handling failed")
	}
	if !kc.handleEvent(backend, change) {
		t.Fatal("reaction handling failed")
	}
	if queued != 2 {
		t.Fatalf("queued = %d, want 2; ignored handling advanced revision", queued)
	}
}

func TestReactionEventPersistsAppliedMembersRevision(t *testing.T) {
	backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, members: reactions.MembersResponse{
		Revision: 12,
		Members:  map[reactions.Type][]int64{reactions.Heart: {42}},
		Fields:   map[string]json.RawMessage{"1": []byte(`[42]`)},
	}}
	kc, harness := newTestClient(t, func() (kakaoClient, error) { return backend, nil })
	kc.client = backend
	if !kc.handleEvent(backend, events.ReactionChanged{ChatID: testChatID, LogID: 99, Revision: 9}) {
		t.Fatal("reaction event was not handled")
	}
	if len(harness.queued) != 1 {
		t.Fatalf("queued = %d, want 1", len(harness.queued))
	}
	syncEvent := harness.queued[0].(*kakaoReactionSync)
	if syncEvent.AppliedRevision != 12 {
		t.Fatalf("applied revision = %d, want 12", syncEvent.AppliedRevision)
	}
	if !kc.handleEvent(backend, events.ReactionChanged{ChatID: testChatID, LogID: 99, Revision: 10}) {
		t.Fatal("stale event was not handled")
	}
	if len(harness.queued) != 1 {
		t.Fatalf("stale event queued again: %d", len(harness.queued))
	}
}

func TestReactionRevisionSuppressesStaleEventAfterSQLiteReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reaction.db")
	raw, err := dbutil.NewWithDialect(path, "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	db := database.New("test", (&KakaoConnector{}).GetDBMetaTypes(), raw)
	if err := db.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.RawDB.ExecContext(ctx, `INSERT INTO ghost (bridge_id,id,name,avatar_id,avatar_hash,avatar_mxc,name_set,avatar_set,contact_info_set,is_bot,identifiers,extra_profile,metadata) VALUES ('test','2000','sender','','','','',0,0,0,'[]',NULL,'{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.RawDB.ExecContext(ctx, `INSERT INTO portal (bridge_id,id,receiver,mxid,parent_id,parent_receiver,relay_bridge_id,relay_login_id,other_user_id,name,topic,avatar_id,avatar_hash,avatar_mxc,name_set,avatar_set,topic_set,name_is_custom,in_space,message_request,room_type,disappear_type,disappear_timer,cap_state,metadata) VALUES ('test','3000','1000',NULL,NULL,'','','','', '', '', '', '', '',0,0,0,0,0,0,'',NULL,NULL,NULL,'{}')`); err != nil {
		t.Fatal(err)
	}
	message := &database.Message{
		BridgeID: "test", ID: makeMessageID(testChatID, 99), PartID: "0", MXID: "$event",
		Room: makePortalKey(testChatID, makeUserLoginID(testSelfID)), SenderID: makeUserID(testOtherID), SenderMXID: "@sender:test",
		Timestamp: time.Unix(1700000000, 0), Metadata: newKakaoMessageMetadata(testChatID, 99, testOtherID, 1, "", 0),
	}
	if err := db.Message.Insert(ctx, message); err != nil {
		t.Fatal(err)
	}
	backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, members: reactions.MembersResponse{
		Revision: 12, Members: map[reactions.Type][]int64{reactions.Heart: {42}}, Fields: map[string]json.RawMessage{"1": []byte(`[42]`)},
	}}
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{}, ID: makeUserLoginID(testSelfID)}, Bridge: &bridgev2.Bridge{DB: db}}
	kc := newKakaoClient(login, testSelfID, nil)
	queued := 0
	kc.queue = func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		queued++
		return bridgev2.EventHandlingResultSuccess
	}
	kc.client = backend
	if !kc.handleEvent(backend, events.ReactionChanged{ChatID: testChatID, LogID: 99, Revision: 9}) || queued != 1 {
		t.Fatalf("initial handle queued=%d", queued)
	}
	if err := raw.RawDB.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := dbutil.NewWithDialect(path, "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.RawDB.Close() }()
	reopenedDB := database.New("test", (&KakaoConnector{}).GetDBMetaTypes(), reopened)
	login2 := &bridgev2.UserLogin{UserLogin: &database.UserLogin{Metadata: &UserLoginMetadata{}, ID: makeUserLoginID(testSelfID)}, Bridge: &bridgev2.Bridge{DB: reopenedDB}}
	kc2 := newKakaoClient(login2, testSelfID, nil)
	kc2.queue = func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		queued++
		return bridgev2.EventHandlingResultSuccess
	}
	kc2.client = backend
	if !kc2.handleEvent(backend, events.ReactionChanged{ChatID: testChatID, LogID: 99, Revision: 10}) {
		t.Fatal("stale event was not handled")
	}
	if queued != 1 {
		t.Fatalf("stale event was queued after reopen: %d", queued)
	}
}

func TestMatrixReactionSendAndStaleRemoveAreBounded(t *testing.T) {
	backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, members: reactions.MembersResponse{
		Revision: 1,
		Members:  map[reactions.Type][]int64{reactions.Heart: {testSelfID}},
	}}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return backend, nil })
	kc.login.UserMXID = id.UserID("@self:test")
	kc.client = backend
	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, kc.login.ID)}}
	target := &database.Message{
		ID:       makeMessageID(testChatID, 99),
		SenderID: makeUserID(testOtherID),
		Metadata: newKakaoMessageMetadata(testChatID, 99, testOtherID, 1, "", 0),
	}
	msg := &bridgev2.MatrixReaction{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.ReactionEventContent]{
			Event:   &event.Event{Sender: kc.login.UserMXID, Content: event.Content{Parsed: &event.ReactionEventContent{RelatesTo: event.RelatesTo{Key: "❤️"}}}},
			Content: &event.ReactionEventContent{RelatesTo: event.RelatesTo{Key: "❤️"}},
			Portal:  portal,
		},
		TargetMessage: target,
	}
	pre, err := kc.PreHandleMatrixReaction(context.Background(), msg)
	if err != nil || pre.MaxReactions != 0 || pre.EmojiID != "kakao:legacy:1" {
		t.Fatalf("pre = %#v, err=%v", pre, err)
	}
	if _, err = kc.HandleMatrixReaction(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if len(backend.requests) != 1 || backend.requests[0].Type != reactions.Heart {
		t.Fatalf("requests = %#v", backend.requests)
	}
	remove := &bridgev2.MatrixReactionRemove{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.RedactionEventContent]{
			Event: &event.Event{Sender: kc.login.UserMXID}, Portal: portal,
		},
		TargetReaction: &database.Reaction{MessageID: target.ID, SenderID: makeUserID(testSelfID), EmojiID: "kakao:legacy:1"},
	}
	if err := kc.HandleMatrixReactionRemove(context.Background(), remove); err != nil {
		t.Fatal(err)
	}
	if len(backend.requests) != 2 || backend.requests[1].Type != reactions.Cancel {
		t.Fatalf("cancel requests = %#v", backend.requests)
	}
	backend.members.Members = map[reactions.Type][]int64{reactions.Like: {testSelfID}}
	if err := kc.HandleMatrixReactionRemove(context.Background(), remove); err != nil {
		t.Fatal(err)
	}
	if len(backend.requests) != 2 {
		t.Fatalf("stale remove canceled replacement: %#v", backend.requests)
	}
}

func TestMatrixReactionMutationAmbiguousTransportIsSentOnce(t *testing.T) {
	backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, err: nil}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return backend, nil })
	kc.login.UserMXID = id.UserID("@self:test")
	kc.client = backend
	backend.mutationErr = errors.New("synthetic ambiguous transport")
	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, kc.login.ID)}}
	msg := &bridgev2.MatrixReaction{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.ReactionEventContent]{
			Event:   &event.Event{Sender: kc.login.UserMXID},
			Content: &event.ReactionEventContent{RelatesTo: event.RelatesTo{Key: "👍"}},
			Portal:  portal,
		},
		TargetMessage: &database.Message{ID: makeMessageID(testChatID, 99)},
	}
	if _, err := kc.HandleMatrixReaction(context.Background(), msg); !errors.Is(err, errReactionMutation) {
		t.Fatalf("error = %v, want sanitized mutation error", err)
	}
	if len(backend.requests) != 1 {
		t.Fatalf("mutation requests = %d, want 1", len(backend.requests))
	}
}

func TestMatrixReactionMutationErrorCategoriesAreRedacted(t *testing.T) {
	const secret = "https://example.invalid/reaction?access_token=synthetic-secret"
	tests := []struct {
		name       string
		cause      error
		category   error
		reason     error
		contextErr error
	}{
		{name: "transport", cause: errors.New(secret), category: reactions.ErrOutcomeUnknown},
		{name: "wrapped transport", cause: fmt.Errorf("%w: %s", macweb.ErrTransport, secret), category: reactions.ErrOutcomeUnknown, reason: macweb.ErrTransport},
		{name: "rejected", cause: reactions.ErrRejected, category: reactions.ErrOutcomeUnconfirmed, reason: reactions.ErrRejected},
		{name: "wrapped rejected", cause: fmt.Errorf("%w: %s", reactions.ErrRejected, secret), category: reactions.ErrOutcomeUnconfirmed, reason: reactions.ErrRejected},
		{name: "malformed", cause: reactions.ErrInvalidResponse, category: reactions.ErrOutcomeUnknown, reason: reactions.ErrInvalidResponse},
		{name: "wrapped malformed", cause: fmt.Errorf("%w: %s", reactions.ErrInvalidResponse, secret), category: reactions.ErrOutcomeUnknown, reason: reactions.ErrInvalidResponse},
		{name: "canceled", cause: context.Canceled, category: reactions.ErrOutcomeUnknown, contextErr: context.Canceled},
		{name: "wrapped canceled", cause: fmt.Errorf("%w: %s", context.Canceled, secret), category: reactions.ErrOutcomeUnknown, contextErr: context.Canceled},
		{name: "deadline", cause: context.DeadlineExceeded, category: reactions.ErrOutcomeUnknown, contextErr: context.DeadlineExceeded},
		{name: "invalid request", cause: reactions.ErrInvalidRequest, category: reactions.ErrInvalidRequest, reason: reactions.ErrInvalidRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, mutationErr: tc.cause}
			kc, _ := newTestClient(t, func() (kakaoClient, error) { return backend, nil })
			kc.login.UserMXID = id.UserID("@self:test")
			kc.client = backend
			portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, kc.login.ID)}}
			msg := &bridgev2.MatrixReaction{
				MatrixEventBase: bridgev2.MatrixEventBase[*event.ReactionEventContent]{
					Event:   &event.Event{Sender: kc.login.UserMXID},
					Content: &event.ReactionEventContent{RelatesTo: event.RelatesTo{Key: "👍"}},
					Portal:  portal,
				},
				TargetMessage: &database.Message{ID: makeMessageID(testChatID, 99)},
			}
			_, err := kc.HandleMatrixReaction(context.Background(), msg)
			if !errors.Is(err, errReactionMutation) || !errors.Is(err, tc.category) {
				t.Fatalf("error = %v, want mutation + category", err)
			}
			if tc.contextErr != nil && !errors.Is(err, tc.contextErr) {
				t.Fatalf("error = %v, want context identity", err)
			}
			if tc.reason != nil && !errors.Is(err, tc.reason) {
				t.Fatalf("error = %v, want safe reason identity", err)
			}
			for _, rendered := range []string{fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
				if strings.Contains(rendered, secret) {
					t.Fatalf("error rendering leaked backend detail: %q", rendered)
				}
			}
			if len(backend.requests) != 1 {
				t.Fatalf("mutation requests = %d, want 1", len(backend.requests))
			}
		})
	}
}

func TestMatrixReactionRemoveLookupFailureIsDistinctAndDoesNotMutate(t *testing.T) {
	backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, err: errors.New("lookup https://example.invalid token=secret")}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return backend, nil })
	kc.login.UserMXID = id.UserID("@self:test")
	kc.client = backend
	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, kc.login.ID)}}
	remove := &bridgev2.MatrixReactionRemove{
		MatrixEventBase: bridgev2.MatrixEventBase[*event.RedactionEventContent]{Event: &event.Event{Sender: kc.login.UserMXID}, Portal: portal},
		TargetReaction:  &database.Reaction{MessageID: makeMessageID(testChatID, 99), SenderID: makeUserID(testSelfID), EmojiID: "kakao:legacy:1"},
	}
	err := kc.HandleMatrixReactionRemove(context.Background(), remove)
	if !errors.Is(err, errReactionLookup) || !errors.Is(err, reactions.ErrLookupFailed) {
		t.Fatalf("error = %v, want distinct lookup category", err)
	}
	if len(backend.requests) != 0 {
		t.Fatalf("mutation requests after lookup failure = %d, want 0", len(backend.requests))
	}
	if strings.Contains(fmt.Sprintf("%#v", err), "token=secret") {
		t.Fatalf("lookup error leaked backend detail: %#v", err)
	}
}

func TestMatrixReactionRemoveMutationCategoriesAreSingleAttempt(t *testing.T) {
	causes := []struct {
		name     string
		cause    error
		category error
		reason   error
	}{
		{name: "transport", cause: fmt.Errorf("%w: dropped", macweb.ErrTransport), category: reactions.ErrOutcomeUnknown, reason: macweb.ErrTransport},
		{name: "rejected", cause: reactions.ErrRejected, category: reactions.ErrOutcomeUnconfirmed, reason: reactions.ErrRejected},
		{name: "canceled", cause: context.Canceled, category: reactions.ErrOutcomeUnknown, reason: context.Canceled},
	}
	for _, tc := range causes {
		t.Run(tc.name, func(t *testing.T) {
			backend := &reactionTestBackend{
				fakeKakao:   &fakeKakao{},
				members:     reactions.MembersResponse{Revision: 1, Members: map[reactions.Type][]int64{reactions.Heart: {testSelfID}}},
				mutationErr: tc.cause,
			}
			kc, _ := newTestClient(t, func() (kakaoClient, error) { return backend, nil })
			kc.login.UserMXID = id.UserID("@self:test")
			kc.client = backend
			portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, kc.login.ID)}}
			remove := &bridgev2.MatrixReactionRemove{
				MatrixEventBase: bridgev2.MatrixEventBase[*event.RedactionEventContent]{Event: &event.Event{Sender: kc.login.UserMXID}, Portal: portal},
				TargetReaction:  &database.Reaction{MessageID: makeMessageID(testChatID, 99), SenderID: makeUserID(testSelfID), EmojiID: "kakao:legacy:1"},
			}
			err := kc.HandleMatrixReactionRemove(context.Background(), remove)
			if !errors.Is(err, errReactionMutation) || !errors.Is(err, tc.category) || !errors.Is(err, tc.reason) {
				t.Fatalf("error = %v, want mutation/category/reason", err)
			}
			if backend.memberCalls != 1 || len(backend.requests) != 1 || backend.requests[0].Type != reactions.Cancel {
				t.Fatalf("lookup/mutation counts = %d/%d, want 1/1", backend.memberCalls, len(backend.requests))
			}
		})
	}
}
