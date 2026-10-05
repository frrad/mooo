package connector

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/reactions"
)

type reactionTestBackend struct {
	*fakeKakao
	members     reactions.MembersResponse
	err         error
	requests    []reactions.Request
	mutationErr error
}

func (b *reactionTestBackend) React(_ context.Context, request reactions.Request) (reactions.Response, error) {
	b.requests = append(b.requests, request)
	if b.mutationErr != nil {
		return reactions.Response{}, b.mutationErr
	}
	return reactions.Response{Status: 0}, nil
}

func (b *reactionTestBackend) ReactionMembers(context.Context, int64, int64) (reactions.MembersResponse, error) {
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
	if !kc.handleEvent(backend, change) || !kc.handleEvent(backend, change) {
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
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: makeUserLoginID(testSelfID)}, Bridge: &bridgev2.Bridge{DB: db}}
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
	login2 := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: makeUserLoginID(testSelfID)}, Bridge: &bridgev2.Bridge{DB: reopenedDB}}
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
	if err != nil || pre.MaxReactions != 1 || pre.EmojiID != "kakao:legacy:1" {
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
