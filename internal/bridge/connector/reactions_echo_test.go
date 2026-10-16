package connector

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/simplevent"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/reactions"
)

// An own reaction sent from Matrix echoes back from Kakao. The framework
// replaces the user's previous reaction row while handling the Matrix event,
// so by delivery time the planned addition is a duplicate and the planned
// removal has no target. Matrix already holds the aggregate's state, which is
// an applied change: the revision must advance (#409).
func TestOwnReactionEchoPersistsRevisionWhenFrameworkAlreadyReplacedRow(t *testing.T) {
	ctx := context.Background()
	intent := &recordingIntent{}
	kc, portal, login, old := newFrameworkReactionFixture(t, intent)
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
		Members:  map[reactions.Type][]int64{reactions.Like: {testSelfID}},
		Fields:   map[string]json.RawMessage{"2": []byte(`[1000]`)},
	}}
	kc.client = backend
	login.Client = kc
	var delivered []bridgev2.RemoteEvent
	var results []bridgev2.EventHandlingResult
	kc.queue = func(evt bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		if len(delivered) == 0 {
			// The framework finishes handling the Matrix reaction after the
			// delivery plan was built: the Heart row is replaced by Like.
			if err := kc.login.Bridge.DB.Reaction.Delete(ctx, old); err != nil {
				t.Fatal(err)
			}
			replacement := &database.Reaction{Room: old.Room, MessageID: old.MessageID, MessagePartID: old.MessagePartID, SenderID: makeUserID(testSelfID), SenderMXID: intent.GetMXID(), EmojiID: "kakao:legacy:2", MXID: "$new", Timestamp: time.Now()}
			if err := kc.login.Bridge.DB.Reaction.Upsert(ctx, replacement); err != nil {
				t.Fatal(err)
			}
		}
		delivered = append(delivered, evt)
		result := (*bridgev2.PortalInternals)(portal).HandleRemoteEvent(ctx, login, evt.GetType(), evt)
		results = append(results, result)
		return result
	}
	if !kc.handleEvent(backend, events.ReactionChanged{ChatID: testChatID, LogID: 99, Revision: 2}) {
		t.Fatal("own-reaction echo was not acknowledged")
	}
	for i, result := range results {
		if !result.Ignored {
			t.Fatalf("delivery %d (%s) was not ignored: %+v", i, delivered[i].GetType(), result)
		}
	}
	for _, evt := range delivered {
		if reaction, ok := evt.(*simplevent.Reaction); ok && reaction.Type == bridgev2.RemoteEventReactionRemove {
			t.Fatalf("removal of an already replaced row was delivered: %+v", reaction)
		}
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
	if revision != 2 {
		t.Fatalf("all-ignored echo left reaction_revision at %d, want 2", revision)
	}
}
