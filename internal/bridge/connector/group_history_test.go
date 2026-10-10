package connector

import (
	"context"
	"errors"
	"fmt"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
	"testing"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

type historyTestSource struct {
	*countingGroupCreateClient
	pages          []client.HistoryPage
	calls          []int64
	visibleTargets []syncmsg.Target
}

func (h *historyTestSource) InitialSyncTargets(context.Context) ([]syncmsg.Target, error) {
	return h.visibleTargets, nil
}

func (h *historyTestSource) ReadHistoryPage(_ context.Context, chatID, after, through int64, limit int) (client.HistoryPage, error) {
	h.calls = append(h.calls, after)
	if len(h.pages) == 0 {
		return client.HistoryPage{}, errors.New("synthetic unavailable history")
	}
	page := h.pages[0]
	h.pages = h.pages[1:]
	return page, nil
}

// Internal.HandleRemoteEvent bypasses the SDK queue's getEventCtxWithLog.
// Invoke the production event hook, as the normal queue does, before entering
// the internal handler used by this synchronous framework harness.
func historyFrameworkContext(ctx context.Context, remote bridgev2.RemoteEvent) context.Context {
	if mutation, ok := remote.(bridgev2.RemoteEventWithContextMutation); ok {
		return mutation.MutateContext(ctx)
	}
	return ctx
}

func newHistoryTest(t *testing.T) (*KakaoClient, *historyTestSource) {
	t.Helper()
	kc, backend, _ := newGroupCreationFramework(t)
	_, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}})
	if err != nil {
		t.Fatal(err)
	}
	backend.chatInfo.ChatData.LastServerLogID = 103
	source := &historyTestSource{countingGroupCreateClient: backend}
	kc.client = source
	return kc, source
}
func TestGroupHistoryProgressSurvivesDeliveryFailureAndExplicitResume(t *testing.T) {
	kc, source := newHistoryTest(t)
	source.pages = []client.HistoryPage{{Events: []events.Event{events.TextMessage{ChatID: 5000, LogID: 101, AuthorID: 2000, Message: "first"}, events.TextMessage{ChatID: 5000, LogID: 103, AuthorID: 4000, Message: "second"}}, Next: 103, Complete: true}, {Events: []events.Event{events.TextMessage{ChatID: 5000, LogID: 103, AuthorID: 4000, Message: "second"}}, Next: 103, Complete: true}}
	p, err := kc.login.Bridge.GetPortalByMXID(t.Context(), "!selected:test")
	if err != nil {
		t.Fatal(err)
	}
	var delivered []string
	fail := true
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		if remote.GetType() != bridgev2.RemoteEventMessage {
			//nolint:staticcheck // Exercise framework membership reconciliation.
			return p.Internal().HandleRemoteEvent(historyFrameworkContext(t.Context(), remote), kc.login, remote.GetType(), remote)
		}
		message := remote.(bridgev2.RemoteMessage)
		if message.GetID() == makeMessageID(5000, 103) && fail {
			return bridgev2.EventHandlingResult{Error: errors.New("synthetic delivery failure")}
		}
		delivered = append(delivered, string(message.GetID()))
		return bridgev2.EventHandlingResult{Success: true}
	}
	if _, err = kc.BackfillGroup(t.Context(), "!selected:test", 100, 103, 10, false); err == nil {
		t.Fatal("failed delivery reported completion")
	}
	state, found, err := kc.loadGroupHistory(t.Context(), "kakao:group-history:1000:5000")
	if err != nil || !found || state.After != 101 || state.Remaining != 9 || state.Done {
		t.Fatalf("durable progress: %+v %t %v", state, found, err)
	}
	if _, err = kc.BackfillGroup(t.Context(), "!selected:test", 100, 103, 10, false); err == nil {
		t.Fatal("new action overwrote unfinished selection")
	}
	fail = false
	reloaded := newKakaoClient(kc.login, kc.userID, nil)
	reloaded.client = source
	reloaded.queue = kc.queue
	kc.login.Client = reloaded
	if done, err := reloaded.BackfillGroup(t.Context(), "!selected:test", 0, 0, 0, true); err != nil || !done {
		t.Fatalf("resume: %t %v", done, err)
	}
	if len(delivered) != 2 || len(source.calls) != 2 || source.calls[0] != 100 || source.calls[1] != 101 {
		t.Fatalf("resume replayed confirmed history: %v/%v", delivered, source.calls)
	}
	if len(source.commits) != 0 {
		t.Fatal("history committed live events")
	}
}

func TestGroupHistoryRejectsInconsistentPageBeforeDelivery(t *testing.T) {
	kc, source := newHistoryTest(t)
	source.pages = []client.HistoryPage{{Events: []events.Event{events.TextMessage{ChatID: 5000, LogID: 101, AuthorID: 2000, Message: "synthetic"}}, Next: 103, Complete: true}}
	p, err := kc.login.Bridge.GetPortalByMXID(t.Context(), "!selected:test")
	if err != nil {
		t.Fatal(err)
	}
	deliveries := 0
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		if remote.GetType() == bridgev2.RemoteEventMessage {
			deliveries++
			return bridgev2.EventHandlingResult{Success: true}
		}
		//nolint:staticcheck // Exercise actual membership consumer.
		return p.Internal().HandleRemoteEvent(historyFrameworkContext(t.Context(), remote), kc.login, remote.GetType(), remote)
	}
	if _, err = kc.BackfillGroup(t.Context(), "!selected:test", 100, 103, 10, false); err == nil {
		t.Fatal("inconsistent cursor accepted")
	}
	state, found, err := kc.loadGroupHistory(t.Context(), "kakao:group-history:1000:5000")
	if err != nil || !found || state.After != 100 || deliveries != 0 {
		t.Fatalf("invalid page changed delivery/progress: %+v %d %v", state, deliveries, err)
	}
}

func (i *groupCreationIntent) SendMessage(ctx context.Context, room id.RoomID, _ event.Type, content *event.Content, _ *bridgev2.MatrixSendExtra) (*mautrix.RespSendEvent, error) {
	// ASIntent sends through IntentAPI.SendMessageEvent, which ensures membership.
	if err := i.EnsureJoined(ctx, room); err != nil {
		return nil, err
	}
	i.m.mu.Lock()
	defer i.m.mu.Unlock()
	body := content.Parsed.(*event.MessageEventContent).Body
	if body == i.m.failMessage {
		return nil, errors.New("synthetic message send failure")
	}
	i.m.messageBodies = append(i.m.messageBodies, body)
	return &mautrix.RespSendEvent{EventID: id.EventID(fmt.Sprintf("$history-%d:test", len(i.m.messageBodies)))}, nil
}

func TestGroupHistoryRealFrameworkDeduplicatesLiveOverlap(t *testing.T) {
	kc, source := newHistoryTest(t)
	p, err := kc.login.Bridge.GetPortalByMXID(t.Context(), "!selected:test")
	if err != nil {
		t.Fatal(err)
	}
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		//nolint:staticcheck // Use the actual consumer and durable message mapping.
		return p.Internal().HandleRemoteEvent(historyFrameworkContext(t.Context(), remote), kc.login, remote.GetType(), remote)
	}
	first := events.TextMessage{ChatID: 5000, LogID: 101, AuthorID: 2000, Message: "overlap", SentAt: 1700000000}
	last := events.TextMessage{ChatID: 5000, LogID: 103, AuthorID: 4000, Message: "history", SentAt: 1700000001}
	if result := kc.queue(kc.remoteEventFor(first)); !committable(result) {
		t.Fatalf("initial live delivery: %+v", result)
	}
	source.pages = []client.HistoryPage{{Events: []events.Event{first, last}, Next: 103, Complete: true}}
	if done, err := kc.BackfillGroup(t.Context(), "!selected:test", 100, 103, 10, false); err != nil || !done {
		t.Fatalf("history: %t %v", done, err)
	}
	for _, logID := range []int64{101, 103} {
		row, err := kc.login.Bridge.DB.Message.GetPartByID(t.Context(), kc.login.ID, makeMessageID(5000, logID), "")
		if err != nil || row == nil || row.MXID == "" {
			t.Fatalf("message mapping %d: %+v %v", logID, row, err)
		}
	}
	matrix := kc.login.Bridge.Matrix.(*groupCreationMatrix)
	matrix.mu.Lock()
	defer matrix.mu.Unlock()
	if len(matrix.messageBodies) != 2 || matrix.messageBodies[0] != "overlap" || matrix.messageBodies[1] != "history" {
		t.Fatalf("duplicate or reordered sends: %v", matrix.messageBodies)
	}
}

func TestGroupHistoryFormerSenderDoesNotRemainInCurrentRoster(t *testing.T) {
	kc, source := newHistoryTest(t)
	p, err := kc.login.Bridge.GetPortalByMXID(t.Context(), "!selected:test")
	if err != nil {
		t.Fatal(err)
	}
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		//nolint:staticcheck // Exercise ghost sender and membership effects.
		return p.Internal().HandleRemoteEvent(historyFrameworkContext(t.Context(), remote), kc.login, remote.GetType(), remote)
	}
	source.pages = []client.HistoryPage{{Events: []events.Event{events.TextMessage{ChatID: 5000, LogID: 103, AuthorID: 3000, Message: "former member", SentAt: 1700000000}}, Next: 103, Complete: true}}
	if done, err := kc.BackfillGroup(t.Context(), "!selected:test", 100, 103, 10, false); err != nil || !done {
		t.Fatalf("former history: %t %v", done, err)
	}
	matrix := kc.login.Bridge.Matrix.(*groupCreationMatrix)
	matrix.mu.Lock()
	defer matrix.mu.Unlock()
	if member := matrix.members["@kakao_3000:test"]; member != nil && member.Membership == event.MembershipJoin {
		t.Fatal("historical author rejoined the current Matrix roster")
	}
	row, err := kc.login.Bridge.DB.Message.GetPartByID(t.Context(), kc.login.ID, makeMessageID(5000, 103), "")
	if err != nil || row == nil || row.SenderID != "3000" {
		t.Fatalf("former sender attribution lost: %+v %v", row, err)
	}
}

func TestGroupHistoryFailedFormerSenderRestoresRosterWithoutAdvancing(t *testing.T) {
	kc, source := newHistoryTest(t)
	p, err := kc.login.Bridge.GetPortalByMXID(t.Context(), "!selected:test")
	if err != nil {
		t.Fatal(err)
	}
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		//nolint:staticcheck // Exercise real failed send and its membership side effect.
		return p.Internal().HandleRemoteEvent(historyFrameworkContext(t.Context(), remote), kc.login, remote.GetType(), remote)
	}
	matrix := kc.login.Bridge.Matrix.(*groupCreationMatrix)
	matrix.failMessage = "failed former text"
	source.pages = []client.HistoryPage{{Events: []events.Event{events.TextMessage{ChatID: 5000, LogID: 103, AuthorID: 3000, Message: matrix.failMessage, SentAt: 1700000000}}, Next: 103, Complete: true}}
	if _, err = kc.BackfillGroup(t.Context(), "!selected:test", 100, 103, 10, false); err == nil {
		t.Fatal("failed send reported success")
	}
	matrix.mu.Lock()
	member := matrix.members["@kakao_3000:test"]
	matrix.mu.Unlock()
	if member != nil && member.Membership == event.MembershipJoin {
		t.Fatal("failed historical send left former ghost joined")
	}
	state, found, err := kc.loadGroupHistory(t.Context(), "kakao:group-history:1000:5000")
	if err != nil || !found || state.After != 100 || state.Done {
		t.Fatalf("failed history advanced: %+v %v", state, err)
	}
}

// An owned post-read observation had no CHATINFO log ceiling, while full
// LOGINLIST still returned the room's current last log and three-member roster.
func TestGroupHistoryReadRoomUsesVerifiedLoginInventoryCeiling(t *testing.T) {
	kc, source := newHistoryTest(t)
	source.chatInfo.ChatData.LastServerLogID = 0
	source.chatInfo.ChatData.LastChatLog = nil
	source.visibleTargets = []syncmsg.Target{{ChatID: 5000, MaxLogID: 103}}
	source.pages = []client.HistoryPage{{Events: []events.Event{events.TextMessage{ChatID: 5000, LogID: 103, AuthorID: 2000, Message: "read-room history"}}, Next: 103, Complete: true}}
	done, err := kc.BackfillGroup(t.Context(), "!selected:test", 100, 103, 10, false)
	if err != nil || !done {
		t.Fatalf("source-visible read-room history rejected: done=%t err=%v", done, err)
	}
	p, err := kc.login.Bridge.GetPortalByMXID(t.Context(), "!selected:test")
	if err != nil {
		t.Fatal(err)
	}
	if p == nil {
		t.Fatal("selected portal missing")
	}
	message, err := kc.login.Bridge.DB.Message.GetPartByID(t.Context(), kc.login.ID, makeMessageID(5000, 103), "")
	if err != nil || message == nil {
		t.Fatalf("historical delivery not mapped: %v", err)
	}
}

func TestGroupHistoryInventoryDoesNotAuthorizeAnotherRoomOrHigherBound(t *testing.T) {
	for _, tc := range []struct {
		name    string
		targets []syncmsg.Target
	}{
		{"other room", []syncmsg.Target{{ChatID: 5001, MaxLogID: 999}}},
		{"lower bound", []syncmsg.Target{{ChatID: 5000, MaxLogID: 102}}},
		{"missing room", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kc, source := newHistoryTest(t)
			source.chatInfo.ChatData.LastServerLogID = 0
			source.chatInfo.ChatData.LastChatLog = nil
			source.visibleTargets = tc.targets
			if _, err := kc.BackfillGroup(t.Context(), "!selected:test", 100, 103, 10, false); err == nil {
				t.Fatal("unverified interval authorized")
			}
			if len(source.calls) != 0 {
				t.Fatal("unverified history requested")
			}
		})
	}
}
