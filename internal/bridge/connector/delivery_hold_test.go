package connector

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/status"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
)

// committedLogs returns the committed log IDs of one chat, in commit order.
func committedLogs(f *fakeKakao, chatID int64) []int64 {
	var logs []int64
	for _, evt := range f.committed() {
		if chat, logID, ok := events.MessagePosition(evt); ok && chat == chatID {
			logs = append(logs, logID)
		}
	}
	return logs
}

// countingOpen counts profile opens and always returns fake, closing its
// stream on Shutdown like the real client.
func countingOpen(fake *fakeKakao, opens *atomic.Int32) func() (kakaoClient, error) {
	return func() (kakaoClient, error) {
		opens.Add(1)
		return &closingFake{fakeKakao: fake}, nil
	}
}

func fakeShutdowns(f *fakeKakao) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shutdownCalls
}

// Finding 2 of research/bridge/NATIVE-SKEW-AUDIT.md: a failed delivery used to
// end the session, and catch-up replayed the event after a full relogin. The
// session now stays up; the chat is held and retried in order while other
// chats keep flowing.
func TestLiveDeliveryFailureHoldsChatWithoutReconnecting(t *testing.T) {
	fake := &fakeKakao{stream: make(chan events.Result, 3)}
	var opens atomic.Int32
	kc, harness := newTestClient(t, countingOpen(fake, &opens))
	kc.deliveryRetryDelays = []time.Duration{time.Millisecond}
	harness.results = []bridgev2.EventHandlingResult{{Error: errors.New("synthetic Matrix outage")}}
	t.Cleanup(kc.Disconnect)
	kc.Connect(t.Context())

	fake.stream <- events.Result{Event: events.TextMessage{ChatID: testChatID, LogID: 11, AuthorID: testOtherID, Message: "failed once"}}
	fake.stream <- events.Result{Event: events.TextMessage{ChatID: testChatID, LogID: 12, AuthorID: testOtherID, Message: "later in the same chat"}}
	fake.stream <- events.Result{Event: events.TextMessage{ChatID: testChatID + 1, LogID: 13, AuthorID: testOtherID, Message: "another chat"}}

	waitFor(t, func() bool {
		return len(committedLogs(fake, testChatID)) == 2 && len(committedLogs(fake, testChatID+1)) == 1
	})
	if got := committedLogs(fake, testChatID); !slices.Equal(got, []int64{11, 12}) {
		t.Fatalf("held chat committed %v, want [11 12]", got)
	}
	if opens.Load() != 1 || fakeShutdowns(fake) != 0 || !kc.IsLoggedIn() {
		t.Fatal("a delivery failure ended the Kakao session")
	}
	if harness.queuedCount() != 4 {
		t.Fatalf("queued %d deliveries, want 4 (one failed attempt and three deliveries)", harness.queuedCount())
	}
	if state := harness.lastState(); state.StateEvent != status.StateConnected {
		t.Fatalf("state = %+v, want connected", state)
	}
}

// When the retry schedule is exhausted the chat pauses: no reconnect, no more
// timed attempts, and a bridge state. The reconnect command retries it in the
// live session.
func TestExhaustedDeliveryPausesChatUntilOperatorResume(t *testing.T) {
	fake := &fakeKakao{stream: make(chan events.Result, 2)}
	var opens atomic.Int32
	kc, harness := newTestClient(t, countingOpen(fake, &opens))
	kc.deliveryRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	failure := bridgev2.EventHandlingResult{Error: errors.New("synthetic persistent failure")}
	harness.results = []bridgev2.EventHandlingResult{failure, failure, failure}
	t.Cleanup(kc.Disconnect)
	kc.Connect(t.Context())

	fake.stream <- events.Result{Event: events.TextMessage{ChatID: testChatID, LogID: 11, AuthorID: testOtherID, Message: "persistent failure"}}
	fake.stream <- events.Result{Event: events.TextMessage{ChatID: testChatID, LogID: 12, AuthorID: testOtherID, Message: "behind it"}}
	waitFor(t, func() bool {
		state := harness.lastState()
		return state.StateEvent == status.StateUnknownError && state.Error == stateChatDeliveryPaused
	})
	time.Sleep(20 * time.Millisecond)
	if harness.queuedCount() != 3 {
		t.Fatalf("queued %d deliveries, want the initial attempt and two retries", harness.queuedCount())
	}
	if len(fake.committed()) != 0 || opens.Load() != 1 || fakeShutdowns(fake) != 0 {
		t.Fatal("a paused chat committed progress or reconnected")
	}

	if got := kc.operatorReconnect(); got != reconnectResumingDelivery {
		t.Fatalf("operatorReconnect() = %v, want reconnectResumingDelivery", got)
	}
	waitFor(t, func() bool { return len(committedLogs(fake, testChatID)) == 2 })
	if got := committedLogs(fake, testChatID); !slices.Equal(got, []int64{11, 12}) {
		t.Fatalf("resumed chat committed %v, want [11 12]", got)
	}
	waitFor(t, func() bool { return harness.lastState().StateEvent == status.StateConnected })
	if opens.Load() != 1 {
		t.Fatal("operator resume opened a new login")
	}
	if got := kc.operatorReconnect(); got != reconnectAlreadyConnected {
		t.Fatalf("operatorReconnect() = %v after resume, want reconnectAlreadyConnected", got)
	}
}

// A failed catch-up delivery used to fail bootstrap and schedule a relogin.
// The chat's remaining interval is now held and the session goes live.
func TestCatchUpDeliveryFailureHoldsChatAndGoesLive(t *testing.T) {
	one := events.TextMessage{ChatID: testChatID, LogID: 11, AuthorID: testOtherID, Message: "first"}
	two := events.TextMessage{ChatID: testChatID, LogID: 12, AuthorID: testOtherID, Message: "second"}
	fake := &fakeKakao{stream: make(chan events.Result), resumeTargets: []syncmsg.Target{{ChatID: testChatID, MaxLogID: 12}}, catchUps: map[int64]catchUpResult{testChatID: {events: []events.Event{one, two}}}}
	var opens atomic.Int32
	kc, harness := newTestClient(t, countingOpen(fake, &opens))
	kc.deliveryRetryDelays = []time.Duration{time.Millisecond}
	harness.results = []bridgev2.EventHandlingResult{{Error: errors.New("synthetic Matrix outage")}}
	t.Cleanup(kc.Disconnect)
	kc.Connect(t.Context())

	if !kc.IsLoggedIn() {
		t.Fatal("a catch-up delivery failure failed the connection")
	}
	waitFor(t, func() bool { return len(committedLogs(fake, testChatID)) == 2 })
	if got := committedLogs(fake, testChatID); !slices.Equal(got, []int64{11, 12}) {
		t.Fatalf("held catch-up committed %v, want [11 12]", got)
	}
	if opens.Load() != 1 {
		t.Fatal("catch-up delivery failure opened another login")
	}
}

// Trigger 1: a transient inbound media download failure.
func TestTransientMediaDownloadFailureRetriesWithoutRelogin(t *testing.T) {
	video, data := videoMessage(t)
	var downloads atomic.Int32
	oldClient := photoHTTPClient
	photoHTTPClient = &http.Client{Transport: photoRoundTripper(func(req *http.Request) (*http.Response, error) {
		if downloads.Add(1) == 1 {
			return nil, errors.New("synthetic CDN connection reset")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})}
	t.Cleanup(func() { photoHTTPClient = oldClient })

	fake := &fakeKakao{stream: make(chan events.Result, 1)}
	var opens atomic.Int32
	kc, _ := newTestClient(t, countingOpen(fake, &opens))
	kc.deliveryRetryDelays = []time.Duration{time.Millisecond}
	var conversions sync.Mutex
	var conversionErrs []error
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		_, err := remote.(bridgev2.RemoteMessage).ConvertMessage(context.Background(), &bridgev2.Portal{Portal: &database.Portal{MXID: "!synthetic:localhost"}}, &albumMatrixAPI{})
		conversions.Lock()
		conversionErrs = append(conversionErrs, err)
		conversions.Unlock()
		if err != nil {
			return bridgev2.EventHandlingResultFailed.WithError(err)
		}
		return bridgev2.EventHandlingResultSuccess
	}
	t.Cleanup(kc.Disconnect)
	kc.Connect(t.Context())

	fake.stream <- events.Result{Event: video}
	waitFor(t, func() bool { return len(committedLogs(fake, testChatID)) == 1 })
	conversions.Lock()
	defer conversions.Unlock()
	if len(conversionErrs) != 2 || !errors.Is(conversionErrs[0], errPhotoTransfer) || conversionErrs[1] != nil {
		t.Fatalf("conversions = %v, want one transient failure then success", conversionErrs)
	}
	if opens.Load() != 1 || fakeShutdowns(fake) != 0 {
		t.Fatal("a media download failure ended the Kakao session")
	}
}

// Trigger 2: GETMSGS for an edit feed that arrived without the edited message.
func TestEditFetchFailureRetriesWithoutRelogin(t *testing.T) {
	fake := &fakeKakao{
		stream:        make(chan events.Result, 1),
		fetchFailures: 1,
		fetchResults:  [][]events.Event{{events.TextMessage{ChatID: testChatID, LogID: 11, AuthorID: testOtherID, Message: "edited", Revision: 1}}},
	}
	var opens atomic.Int32
	kc, harness := newTestClient(t, countingOpen(fake, &opens))
	kc.deliveryRetryDelays = []time.Duration{time.Millisecond}
	t.Cleanup(kc.Disconnect)
	kc.Connect(t.Context())

	fake.stream <- events.Result{Event: events.MessageEdited{ChatID: testChatID, LogID: 20, AuthorID: testOtherID, TargetLogID: 11, TargetRevision: 1}}
	waitFor(t, func() bool { return len(committedLogs(fake, testChatID)) == 1 })
	fake.mu.Lock()
	fetches := len(fake.fetchRequests)
	fake.mu.Unlock()
	if fetches != 2 {
		t.Fatalf("GETMSGS reads = %d, want one failure and one retry", fetches)
	}
	if harness.queuedCount() != 1 {
		t.Fatalf("queued %d edits, want 1", harness.queuedCount())
	}
	if opens.Load() != 1 || fakeShutdowns(fake) != 0 {
		t.Fatal("a GETMSGS failure ended the Kakao session")
	}
}

// Trigger 3: the roster read of a membership notice. A feed the stored roster
// absorbs needs no read; one it cannot (here a self invite) still reads the
// roster. The failure pauses the group and schedules its membership retry;
// the held chat waits for that retry instead of re-reading the roster on its
// own timer, and resumes once membership converges.
func TestMembershipRefreshFailureHoldsChatUntilMembershipConverges(t *testing.T) {
	kc, backend := newManagedGroupReconnect(t)
	kc.deliveryRetryDelays = []time.Duration{time.Millisecond}
	feed := events.MemberAdded{ChatID: 5000, LogID: 200, Members: []events.MemberIdentity{{UserID: kc.userID}}}
	later := events.TextMessage{ChatID: 5000, LogID: 201, AuthorID: 2000, Message: "after the leave"}
	backend.mu.Lock()
	backend.resumeTargets = []syncmsg.Target{{ChatID: 5000, MaxLogID: 201}}
	backend.catchUps = map[int64]catchUpResult{5000: {events: []events.Event{feed, later}}}
	backend.memberListErr = errors.New("synthetic MEMLIST failure")
	backend.mu.Unlock()

	if _, err := kc.connectAndSubscribe(t.Context(), backend); err != nil {
		t.Fatalf("a membership refresh failure failed the connection: %v", err)
	}
	if len(backend.committed()) != 0 {
		t.Fatal("a failed membership feed committed progress")
	}
	if n := rosterReads(backend); n != 1 {
		t.Fatalf("roster reads = %d, want 1", n)
	}
	if _, ok := kc.nextHeldRetry(); ok {
		t.Fatal("a chat waiting for membership retries on its own timer")
	}

	backend.mu.Lock()
	backend.memberListErr = nil
	backend.mu.Unlock()
	kc.mu.Lock()
	kc.client = backend
	kc.mu.Unlock()
	result := kc.performMetadataRefresh(t.Context(), backend, metadataRefreshTask{kind: retryMembership, chatID: 5000})
	if kc.applyMetadataRefresh(backend, result) {
		t.Fatal("membership retry ended the session")
	}
	if got := committedLogs(backend.fakeKakao, 5000); !slices.Equal(got, []int64{200, 201}) {
		t.Fatalf("held chat committed %v after convergence, want [200 201]", got)
	}
}
