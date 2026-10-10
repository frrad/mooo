package connector

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
)

func TestSuppressedMatrixRefusalDoesNotCommitUnmappedMessage(t *testing.T) {
	for _, refusal := range []error{mautrix.MForbidden, mautrix.MNotFound, mautrix.MBadJSON, mautrix.MInvalidParam, mautrix.MBadState} {
		t.Run(refusal.Error(), func(t *testing.T) {
			kc, backend, mx := newGroupCreationFramework(t)
			_, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}})
			if err != nil {
				t.Fatal(err)
			}
			mx.failMessage = "refused"
			mx.failMessageError = refusal
			kc.queue = kc.login.QueueRemoteEvent
			message := events.TextMessage{ChatID: 5000, LogID: 103, AuthorID: 2000, Message: "refused"}
			if kc.handleEvent(backend, message) || len(backend.committed()) != 0 {
				t.Fatal("SDK-suppressed Matrix refusal committed an unmapped source message")
			}
			mx.failMessage = ""
			if !kc.handleEvent(backend, message) || len(backend.committed()) != 1 {
				t.Fatal("restored delivery did not advance progress")
			}
			if !kc.handleEvent(backend, message) {
				t.Fatal("existing mapping did not authorize duplicate reconciliation")
			}
			parts, err := kc.login.Bridge.DB.Message.GetAllPartsByID(t.Context(), kc.login.ID, makeMessageID(5000, 103))
			if err != nil || len(parts) != 1 || len(mx.messageBodies) != 1 {
				t.Fatal("replay duplicated the Matrix event")
			}
		})
	}
}

func TestSuppressedMatrixRefusalDoesNotAdvanceHistoryJournal(t *testing.T) {
	kc, source := newHistoryTest(t)
	mx := kc.login.Bridge.Matrix.(*groupCreationMatrix)
	mx.failMessage = "refused history"
	mx.failMessageError = mautrix.MForbidden
	source.pages = []client.HistoryPage{{Events: []events.Event{events.TextMessage{ChatID: 5000, LogID: 103, AuthorID: 2000, Message: mx.failMessage}}, Next: 103, Complete: true}}
	kc.queue = kc.login.QueueRemoteEvent
	_, err := kc.BackfillGroup(t.Context(), "!selected:test", 100, 103, 10, false)
	if err == nil {
		t.Fatal("SDK-suppressed refusal reported history completion")
	}
	progress, found, err := kc.loadGroupHistory(t.Context(), "kakao:group-history:1000:5000")
	if err != nil || !found || progress.After != 100 || progress.Done {
		t.Fatal("refused history advanced its durable journal")
	}
	mx.failMessage = ""
	source.pages = []client.HistoryPage{{Events: []events.Event{events.TextMessage{ChatID: 5000, LogID: 103, AuthorID: 2000, Message: "refused history"}}, Next: 103, Complete: true}}
	if done, err := kc.BackfillGroup(t.Context(), "!selected:test", 0, 0, 0, true); err != nil || !done {
		t.Fatal("restored historical delivery could not explicitly resume")
	}
	if len(mx.messageBodies) != 1 {
		t.Fatal("history resume duplicated the send")
	}
}

func TestLiveDeliveryFailureStopsAdmissionAndReleasesOwner(t *testing.T) {
	for i, result := range []bridgev2.EventHandlingResult{
		{Error: errors.New("synthetic Matrix outage")},
		bridgev2.EventHandlingResultQueued,
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			fake := &fakeKakao{stream: make(chan events.Result, 2), shutdownEntered: make(chan struct{})}
			kc, harness := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
			harness.results = []bridgev2.EventHandlingResult{result}
			kc.wait = func(ctx context.Context, _ time.Duration) error { <-ctx.Done(); return ctx.Err() }
			defer func() { close(fake.stream); kc.Disconnect() }()
			kc.Connect(t.Context())
			fake.stream <- events.Result{Event: events.TextMessage{ChatID: testChatID, LogID: 11, AuthorID: testOtherID, Message: "failed"}}
			fake.stream <- events.Result{Event: events.TextMessage{ChatID: testChatID, LogID: 12, AuthorID: testOtherID, Message: "later"}}
			select {
			case <-fake.shutdownEntered:
			case <-time.After(time.Second):
				t.Fatal("failed delivery left the live source owner running")
			}
			if harness.queuedCount() != 1 || len(fake.committed()) != 0 {
				t.Fatal("delivery continued past an unconfirmed event")
			}
			if state := harness.lastState(); state.StateEvent != status.StateTransientDisconnect || state.Error != stateDeliveryPaused {
				t.Fatal("failed delivery did not report actionable retained progress")
			}
		})
	}
}

func TestLiveDeliveryRecoveryReplaysBeforeResumingLive(t *testing.T) {
	first := &fakeKakao{stream: make(chan events.Result, 2)}
	one := events.TextMessage{ChatID: testChatID, LogID: 11, AuthorID: testOtherID, Message: "first"}
	two := events.TextMessage{ChatID: testChatID, LogID: 12, AuthorID: testOtherID, Message: "second"}
	second := &fakeKakao{stream: make(chan events.Result), resumeTargets: []syncmsg.Target{{ChatID: testChatID, MaxLogID: 12}}, catchUps: map[int64]catchUpResult{testChatID: {events: []events.Event{one, two}}}}
	var opens atomic.Int32
	kc, harness := newTestClient(t, func() (kakaoClient, error) {
		if opens.Add(1) == 1 {
			return first, nil
		}
		first.mu.Lock()
		closed := first.closeCalls > 0
		first.mu.Unlock()
		if !closed {
			return nil, errors.New("old owner still held")
		}
		return second, nil
	})
	harness.results = []bridgev2.EventHandlingResult{{Error: errors.New("synthetic Matrix outage")}}
	kc.wait = func(context.Context, time.Duration) error { return nil }
	defer func() { close(first.stream); close(second.stream); kc.Disconnect() }()
	kc.Connect(t.Context())
	first.stream <- events.Result{Event: one}
	first.stream <- events.Result{Event: two}
	waitFor(t, func() bool { return opens.Load() == 2 && kc.IsLoggedIn() })
	if len(first.committed()) != 0 {
		t.Fatal("failed session advanced progress")
	}
	commits := second.committed()
	if len(commits) != 2 || commits[0] != one || commits[1] != two {
		t.Fatal("catch-up did not replay in source order")
	}
	if harness.queuedCount() != 3 {
		t.Fatal("later event was delivered before replay")
	}
}

func TestSuppressedMultipartRefusalRetainsProgressAndResumesMissingParts(t *testing.T) {
	kc, backend, mx := newGroupCreationFramework(t)
	if _, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}); err != nil {
		t.Fatal(err)
	}
	kc.queue = kc.login.QueueRemoteEvent
	message := events.MiniTextMessage{TextMessage: events.TextMessage{ChatID: 5000, LogID: 103, AuthorID: 2000, Message: "firstsecond"}, Parts: []events.MiniTextPart{{Text: "first"}, {Text: "second"}}}
	mx.failMessage = "second"
	mx.failMessageError = mautrix.MForbidden
	if kc.handleEvent(backend, message) || len(backend.committed()) != 0 {
		t.Fatal("partially mapped refused message committed source progress")
	}
	parts, err := kc.login.Bridge.DB.Message.GetAllPartsByID(t.Context(), kc.login.ID, makeMessageID(5000, 103))
	if err != nil || len(parts) != 1 || len(mx.messageBodies) != 1 {
		t.Fatal("failure did not retain exactly the successful first part")
	}
	mx.failMessage = ""
	if !kc.handleEvent(backend, message) || len(backend.committed()) != 1 {
		t.Fatal("missing part recovery did not commit the complete message")
	}
	if !kc.handleEvent(backend, message) {
		t.Fatal("complete replay was rejected")
	}
	parts, err = kc.login.Bridge.DB.Message.GetAllPartsByID(t.Context(), kc.login.ID, makeMessageID(5000, 103))
	if err != nil || len(parts) != 2 || len(mx.messageBodies) != 2 || mx.messageBodies[0] != "first" || mx.messageBodies[1] != "second" {
		t.Fatal("recovery lost, reordered or duplicated a part")
	}
}

// A Matrix outage can outlast the first reconnect, so the catch-up replay of
// the paused event fails too. That is the same retained-progress condition as
// the live pause and must keep using the bounded recovery budget instead of
// stopping until an operator reconnects.
func TestCatchUpDeliveryFailureKeepsBoundedRecovery(t *testing.T) {
	one := events.TextMessage{ChatID: testChatID, LogID: 11, AuthorID: testOtherID, Message: "first"}
	first := &fakeKakao{stream: make(chan events.Result, 1)}
	second := &fakeKakao{stream: make(chan events.Result), resumeTargets: []syncmsg.Target{{ChatID: testChatID, MaxLogID: 11}}, catchUps: map[int64]catchUpResult{testChatID: {events: []events.Event{one}}}}
	third := &fakeKakao{stream: make(chan events.Result), resumeTargets: []syncmsg.Target{{ChatID: testChatID, MaxLogID: 11}}, catchUps: map[int64]catchUpResult{testChatID: {events: []events.Event{one}}}}
	owners := []*fakeKakao{first, second, third}
	var opens atomic.Int32
	kc, harness := newTestClient(t, func() (kakaoClient, error) {
		n := int(opens.Add(1))
		if n > len(owners) {
			return nil, errors.New("unexpected extra connection")
		}
		return owners[n-1], nil
	})
	harness.results = []bridgev2.EventHandlingResult{
		{Error: errors.New("synthetic Matrix outage")},
		{Error: errors.New("synthetic Matrix outage continues")},
	}
	kc.wait = func(context.Context, time.Duration) error { return nil }
	defer func() { close(first.stream); close(second.stream); close(third.stream); kc.Disconnect() }()
	kc.Connect(t.Context())
	first.stream <- events.Result{Event: one}
	waitFor(t, func() bool { return opens.Load() == 3 && kc.IsLoggedIn() })
	if len(first.committed()) != 0 || len(second.committed()) != 0 {
		t.Fatal("failed delivery advanced progress")
	}
	if commits := third.committed(); len(commits) != 1 || commits[0] != one {
		t.Fatal("recovery did not replay the paused event once")
	}
	if harness.queuedCount() != 3 {
		t.Fatalf("queued %d deliveries", harness.queuedCount())
	}
}

func TestCatchUpRetriesOnlyUnconfirmedMatrixDelivery(t *testing.T) {
	if !retryableRecoveryError(bootstrapFailure{stage: "catch-up", err: fmt.Errorf("catch-up event: %w", errDeliveryNotConfirmed)}) {
		t.Fatal("unconfirmed catch-up delivery is not retried")
	}
	for _, err := range []error{client.ErrClosed, client.ErrGapUnresolved, errors.New("catch up chat: synthetic source failure")} {
		if retryableRecoveryError(bootstrapFailure{stage: "catch-up", err: err}) {
			t.Fatalf("source-side catch-up failure became retryable: %v", err)
		}
	}
}
