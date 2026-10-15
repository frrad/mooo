package connector

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/reactions"
)

const testThirdID = 3000

// newDerivedReactionFixture is the framework reaction fixture, where
// testOtherID holds a stored heart on message 99, after one full members
// read at revision 5 confirmed that state in this session.
func newDerivedReactionFixture(t *testing.T) (*KakaoClient, *reactionTestBackend) {
	t.Helper()
	ctx := context.Background()
	intent := &recordingIntent{}
	kc, portal, login, _ := newFrameworkReactionFixture(t, intent)
	backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, members: reactions.MembersResponse{
		Revision: 5,
		Members:  map[reactions.Type][]int64{reactions.Heart: {testOtherID}},
		Fields:   map[string]json.RawMessage{"1": []byte(`[2000]`)},
	}}
	kc.client = backend
	login.Client = kc
	attachFrameworkUser(t, login)
	spacedReactionClock(kc)
	kc.queue = func(evt bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		return (*bridgev2.PortalInternals)(portal).HandleRemoteEvent(ctx, login, evt.GetType(), evt)
	}
	if !kc.handleEvent(backend, legacyPush(5, `{"1":1}`, `{"my":0}`)) {
		t.Fatal("confirming push was not handled")
	}
	if backend.memberCalls != 1 {
		t.Fatalf("confirming members reads = %d, want 1", backend.memberCalls)
	}
	return kc, backend
}

func legacyPush(revision int64, counts, extra string) events.ReactionChanged {
	var parsed map[string]int64
	if err := json.Unmarshal([]byte(counts), &parsed); err != nil {
		panic(err)
	}
	keys := make([]string, 0, len(parsed))
	for key := range parsed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	items := make([]events.ReactionItem, 0, len(keys))
	for _, key := range keys {
		items = append(items, events.ReactionItem{ID: key, Kind: 1, Count: parsed[key]})
	}
	// Decode extra through the production decoder.
	synced, err := events.DecodeSyncedLogMeta([]byte(`{"chatId":3000,"logId":99,"type":1,"revision":1,"content":"{}","extra":` + string(mustJSON(extra)) + `}`))
	if err != nil {
		panic(err)
	}
	return events.ReactionChanged{MetadataType: 1, ChatID: testChatID, LogID: 99, Revision: revision, Items: items, Extra: synced.(events.ReactionChanged).Extra}
}

func mustJSON(value string) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

// storedLegacyPicks reads every sender's stored legacy reaction on message 99.
func storedLegacyPicks(t *testing.T, kc *KakaoClient) map[networkid.UserID]networkid.EmojiID {
	t.Helper()
	rows, err := kc.store.reactionsToMessage(context.Background(), kc.login.ID, makeMessageID(testChatID, 99))
	if err != nil {
		t.Fatal(err)
	}
	picks := map[networkid.UserID]networkid.EmojiID{}
	for _, row := range rows {
		picks[row.SenderID] = row.EmojiID
	}
	return picks
}

func storedLegacyRevision(t *testing.T, kc *KakaoClient) int64 {
	t.Helper()
	msg, err := kc.store.firstMessagePart(context.Background(), kc.login.ID, makeMessageID(testChatID, 99))
	if err != nil {
		t.Fatal(err)
	}
	return reactionMetadataRevision(msg.Metadata, events.ReactionChanged{MetadataType: 1})
}

func TestOwnPickFromPushIsAppliedWithoutMembersRead(t *testing.T) {
	kc, backend := newDerivedReactionFixture(t)
	// This account picked Like on another device: the push's own pick and
	// counts account for every stored reaction.
	if !kc.handleEvent(backend, legacyPush(6, `{"1":1,"2":1}`, `{"my":2}`)) {
		t.Fatal("push was not handled")
	}
	if backend.memberCalls != 1 {
		t.Fatalf("members reads = %d, want none after the confirming read", backend.memberCalls)
	}
	want := map[networkid.UserID]networkid.EmojiID{makeUserID(testOtherID): "kakao:legacy:1", makeUserID(testSelfID): "kakao:legacy:2"}
	if got := storedLegacyPicks(t, kc); len(got) != 2 || got[makeUserID(testOtherID)] != want[makeUserID(testOtherID)] || got[makeUserID(testSelfID)] != want[makeUserID(testSelfID)] {
		t.Fatalf("stored picks = %v, want %v", got, want)
	}
	if got := storedLegacyRevision(t, kc); got != 6 {
		t.Fatalf("persisted revision = %d, want 6", got)
	}
	// Cancelling it on that device: own pick 0.
	if !kc.handleEvent(backend, legacyPush(7, `{"1":1}`, `{"my":0}`)) {
		t.Fatal("cancel push was not handled")
	}
	if got := storedLegacyPicks(t, kc); len(got) != 1 || backend.memberCalls != 1 {
		t.Fatalf("stored picks = %v, members reads = %d; want only the other heart, no read", got, backend.memberCalls)
	}
}

func TestActorDeltaFromPushIsAppliedWhenCountsAgree(t *testing.T) {
	kc, backend := newDerivedReactionFixture(t)
	// A third member adds Check; the push names the actor and its selection.
	if !kc.handleEvent(backend, legacyPush(6, `{"1":1,"3":1}`, `{"my":0,"userId":3000,"type":3}`)) {
		t.Fatal("push was not handled")
	}
	got := storedLegacyPicks(t, kc)
	if backend.memberCalls != 1 || len(got) != 2 || got[makeUserID(testThirdID)] != "kakao:legacy:3" {
		t.Fatalf("stored picks = %v, members reads = %d; want the third member's check, no read", got, backend.memberCalls)
	}
	// The other member moves heart to Laugh.
	if !kc.handleEvent(backend, legacyPush(7, `{"3":1,"4":1}`, `{"my":0,"userId":2000,"type":4}`)) {
		t.Fatal("push was not handled")
	}
	got = storedLegacyPicks(t, kc)
	if backend.memberCalls != 1 || got[makeUserID(testOtherID)] != "kakao:legacy:4" {
		t.Fatalf("stored picks = %v, members reads = %d; want the moved laugh, no read", got, backend.memberCalls)
	}
}

func TestUniqueHolderRemovalIsAppliedWithoutActor(t *testing.T) {
	kc, backend := newDerivedReactionFixture(t)
	// The heart count drops to zero and only one member held a heart.
	if !kc.handleEvent(backend, legacyPush(6, `{}`, `{"my":0}`)) {
		t.Fatal("push was not handled")
	}
	if got := storedLegacyPicks(t, kc); len(got) != 0 || backend.memberCalls != 1 {
		t.Fatalf("stored picks = %v, members reads = %d; want none, no read", got, backend.memberCalls)
	}
}

func TestUnattributableOrInconsistentPushReadsMembers(t *testing.T) {
	cases := map[string]events.ReactionChanged{
		// Someone added a heart; the push does not say who.
		"addition without actor": legacyPush(6, `{"1":2}`, `{"my":0}`),
		// The named actor's change does not explain the counts.
		"inconsistent actor": legacyPush(6, `{"1":1,"2":2}`, `{"my":0,"userId":3000,"type":2}`),
		// The own pick and one attributable change do not explain the counts.
		"inconsistent own pick": legacyPush(6, `{"3":1}`, `{"my":2}`),
		// No own pick: nothing anchors the account's selection.
		"no extra": legacyPush(6, `{"1":1,"2":1}`, ``),
	}
	for name, push := range cases {
		t.Run(name, func(t *testing.T) {
			kc, backend := newDerivedReactionFixture(t)
			backend.members.Revision = 6
			if !kc.handleEvent(backend, push) {
				t.Fatal("push was not handled")
			}
			if backend.memberCalls != 2 {
				t.Fatalf("members reads = %d, want the fallback read", backend.memberCalls)
			}
		})
	}
}

func TestDerivationNeedsStateConfirmedInThisSession(t *testing.T) {
	ctx := context.Background()
	intent := &recordingIntent{}
	kc, portal, login, _ := newFrameworkReactionFixture(t, intent)
	backend := &reactionTestBackend{fakeKakao: &fakeKakao{}, members: reactions.MembersResponse{
		Revision: 6,
		Members:  map[reactions.Type][]int64{reactions.Heart: {testOtherID}, reactions.Like: {testSelfID}},
		Fields:   map[string]json.RawMessage{"1": []byte(`[2000]`), "2": []byte(`[1000]`)},
	}}
	kc.client = backend
	login.Client = kc
	attachFrameworkUser(t, login)
	kc.queue = func(evt bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		return (*bridgev2.PortalInternals)(portal).HandleRemoteEvent(ctx, login, evt.GetType(), evt)
	}
	// The stored heart predates this session; changes may have been missed
	// while offline, so the first push reads members even though its counts
	// and own pick agree with the store.
	if !kc.handleEvent(backend, legacyPush(6, `{"1":1,"2":1}`, `{"my":2}`)) {
		t.Fatal("push was not handled")
	}
	if backend.memberCalls != 1 {
		t.Fatalf("members reads = %d, want 1", backend.memberCalls)
	}
}

// attachFrameworkUser gives the fixture login its Matrix user, which the
// framework needs for this account's own reactions.
func attachFrameworkUser(t *testing.T, login *bridgev2.UserLogin) {
	t.Helper()
	user, err := login.Bridge.GetUserByMXID(context.Background(), id.UserID("@owner:test"))
	if err != nil {
		t.Fatal(err)
	}
	login.User = user
	login.UserMXID = user.MXID
}
