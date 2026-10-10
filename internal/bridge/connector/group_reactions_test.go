package connector

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/reactions"
)

// Android's quick reactions arrive as mini items labelled in Korean. Matrix
// receives the same emoji the outbound legacy table uses, while the stored
// identity stays the mini item.
func TestObservedQuickReactionMiniItemsUseEmoji(t *testing.T) {
	raw, err := os.ReadFile("../../../research/fixtures/reactions/observed-android-quick-reactions.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Quick []struct {
			ItemID  string `json:"item_id"`
			LabelKO string `json:"label_ko"`
			Emoji   string `json:"emoji"`
		} `json:"quick_reactions"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Quick) != 6 {
		t.Fatalf("fixture: %v", err)
	}
	change := events.ReactionChanged{MetadataType: 2, ChatID: testChatID, LogID: 300, Revision: 1}
	var details reactions.DetailsResponse
	for i, q := range fixture.Quick {
		change.Items = append(change.Items, events.ReactionItem{Kind: 2, ID: q.ItemID, Count: 1, Alt: map[string]string{"ko": q.LabelKO}})
		details.Details = append(details.Details, reactions.Detail{Kind: 2, ReactionID: q.ItemID, UserIDs: []int64{testOtherID + int64(i)}})
	}
	users := map[networkid.UserID]*bridgev2.ReactionSyncUser{}
	if err := addMiniReactionUsers(users, details, change, testSelfID, makeUserLoginID(testSelfID)); err != nil {
		t.Fatal(err)
	}
	for i, q := range fixture.Quick {
		user := users[makeUserID(testOtherID+int64(i))]
		if user == nil || len(user.Reactions) != 1 {
			t.Fatalf("%s: user reactions %+v", q.ItemID, user)
		}
		got := user.Reactions[0]
		if got.Emoji != q.Emoji || got.EmojiID != networkid.EmojiID("kakao:mini:"+q.ItemID) {
			t.Fatalf("%s: emoji=%q id=%q, want %q", q.ItemID, got.Emoji, got.EmojiID, q.Emoji)
		}
	}
}

// Kakao keeps one legacy selection per user, but mini items are separate. A
// Matrix reaction replaces only the sender's previous legacy reaction; the
// same account's mini reaction (for example added on the phone) stays.
func TestOutboundLegacyReactionKeepsSendersMiniReaction(t *testing.T) {
	ctx := context.Background()
	intent := &frameworkReactionIntent{}
	kc, portal, login, old, raw := newFrameworkReactionFixture(t, intent)
	defer func() { _ = raw.RawDB.Close() }()
	if err := kc.login.Bridge.DB.Reaction.Delete(ctx, old); err != nil {
		t.Fatal(err)
	}
	old.SenderID = makeUserID(testSelfID)
	old.SenderMXID = intent.GetMXID()
	if err := kc.login.Bridge.DB.Reaction.Upsert(ctx, old); err != nil {
		t.Fatal(err)
	}
	mini := *old
	mini.EmojiID = "kakao:mini:1200509_002"
	mini.Emoji = "😮"
	mini.MXID = "$mini"
	if err := kc.login.Bridge.DB.Reaction.Upsert(ctx, &mini); err != nil {
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
	evt := &event.Event{Sender: login.UserMXID, Content: event.Content{Parsed: &event.ReactionEventContent{RelatesTo: event.RelatesTo{Type: event.RelAnnotation, EventID: "$event", Key: "👍"}}}}
	result := (*bridgev2.PortalInternals)(portal).HandleMatrixReaction(ctx, login, evt)
	if !result.Success {
		t.Fatalf("framework outbound reaction failed: %+v", result)
	}
	if len(backend.requests) != 1 || backend.requests[0].Type != reactions.Like {
		t.Fatalf("Kakao mutations = %#v, want one Like", backend.requests)
	}
	if got := countFrameworkEvents(intent.types, event.EventRedaction); got != 1 {
		t.Fatalf("Matrix redactions = %d, want only the replaced legacy reaction", got)
	}
	miniRow, err := kc.login.Bridge.DB.Reaction.GetByIDWithoutMessagePart(ctx, kc.login.ID, old.MessageID, mini.SenderID, mini.EmojiID)
	if err != nil || miniRow == nil {
		t.Fatalf("mini reaction was removed by a legacy reaction: row=%#v err=%v", miniRow, err)
	}
	oldRow, err := kc.login.Bridge.DB.Reaction.GetByIDWithoutMessagePart(ctx, kc.login.ID, old.MessageID, old.SenderID, old.EmojiID)
	if err != nil || oldRow != nil {
		t.Fatalf("replaced legacy reaction row kept: row=%#v err=%v", oldRow, err)
	}
	newRow, err := kc.login.Bridge.DB.Reaction.GetByIDWithoutMessagePart(ctx, kc.login.ID, old.MessageID, makeUserID(testSelfID), "kakao:legacy:2")
	if err != nil || newRow == nil {
		t.Fatalf("new legacy reaction missing: row=%#v err=%v", newRow, err)
	}
}

func syncedMeta(t *testing.T, logID, revision int64, content string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"chatId": testChatID, "logId": logID, "type": 1, "revision": revision, "content": content})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Reaction changes made while the bridge was offline are recovered on the
// next connection from the server's metadata resync, starting at the oldest
// bridged message, and the per-chat cursor advances only after the page is
// applied. Metas for unbridged messages are skipped.
func TestReconnectResyncsReactionsChangedWhileOffline(t *testing.T) {
	ctx := context.Background()
	intent := &frameworkReactionIntent{}
	kc, portal, login, old, raw := newFrameworkReactionFixture(t, intent)
	defer func() { _ = raw.RawDB.Close() }()
	if _, err := raw.RawDB.ExecContext(ctx, `UPDATE portal SET mxid='!room:test'`); err != nil {
		t.Fatal(err)
	}
	if err := kc.login.Bridge.DB.Reaction.Delete(ctx, old); err != nil {
		t.Fatal(err)
	}
	backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, members: reactions.MembersResponse{
		Revision: 500,
		Members:  map[reactions.Type][]int64{reactions.Like: {testOtherID}},
		Fields:   map[string]json.RawMessage{"2": []byte(`[2000]`)},
	}}
	backend.reactionPages = []reactions.SyncMetaPage{{Items: []json.RawMessage{
		syncedMeta(t, 99, 500, `{"2":1}`),
		syncedMeta(t, 1234, 501, `{"1":1}`), // not bridged: skipped
	}, Last: true}}
	kc.client = backend
	login.Client = kc
	kc.queue = func(evt bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		return (*bridgev2.PortalInternals)(portal).HandleRemoteEvent(ctx, login, evt.GetType(), evt)
	}
	kc.resyncReactions(ctx, backend)
	if len(backend.reactionCursors) != 1 || backend.reactionCursors[0] != 99 {
		t.Fatalf("first resync cursors = %v, want the oldest bridged log 99", backend.reactionCursors)
	}
	row, err := kc.login.Bridge.DB.Reaction.GetByIDWithoutMessagePart(ctx, kc.login.ID, makeMessageID(testChatID, 99), makeUserID(testOtherID), "kakao:legacy:2")
	if err != nil || row == nil {
		t.Fatalf("offline reaction not recovered: row=%#v err=%v", row, err)
	}
	// The next connection resumes after the applied page.
	kc.resyncReactions(ctx, backend)
	if len(backend.reactionCursors) != 2 || backend.reactionCursors[1] != 501 {
		t.Fatalf("second resync cursors = %v, want 501", backend.reactionCursors)
	}
	// A failed resync leaves the cursor for the next connection.
	backend.reactionSyncErr = errors.New("synthetic resync failure")
	kc.resyncReactions(ctx, backend)
	backend.reactionSyncErr = nil
	kc.resyncReactions(ctx, backend)
	if got := backend.reactionCursors[len(backend.reactionCursors)-1]; got != 501 {
		t.Fatalf("cursor after failure = %d, want 501", got)
	}
}

func TestResyncFailureInReactionApplyKeepsCursor(t *testing.T) {
	ctx := context.Background()
	intent := &frameworkReactionIntent{}
	kc, portal, login, _, raw := newFrameworkReactionFixture(t, intent)
	defer func() { _ = raw.RawDB.Close() }()
	if _, err := raw.RawDB.ExecContext(ctx, `UPDATE portal SET mxid='!room:test'`); err != nil {
		t.Fatal(err)
	}
	backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, err: errors.New("synthetic members failure")}
	backend.reactionPages = []reactions.SyncMetaPage{{Items: []json.RawMessage{syncedMeta(t, 99, 600, `{"2":1}`)}, Last: true}}
	kc.client = backend
	login.Client = kc
	kc.queue = func(evt bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		if evt.GetType() == bridgev2.RemoteEventMessage {
			// The bounded failure notice; its delivery is not under test.
			return bridgev2.EventHandlingResultSuccess
		}
		return (*bridgev2.PortalInternals)(portal).HandleRemoteEvent(ctx, login, evt.GetType(), evt)
	}
	kc.resyncReactions(ctx, backend)
	kc.resyncReactions(ctx, backend)
	if len(backend.reactionCursors) != 2 || backend.reactionCursors[1] != 99 {
		t.Fatalf("cursors = %v, want unchanged 99 after a failed apply", backend.reactionCursors)
	}
}
