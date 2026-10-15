package connector

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"maunium.net/go/mautrix/bridgev2/status"
)

func TestRecoveryCleansOldLeaseBeforeOpeningReplacement(t *testing.T) {
	first := &fakeKakao{stream: make(chan events.Result)}
	second := &fakeKakao{stream: make(chan events.Result)}
	var opened atomic.Int32
	kc, _ := newTestClient(t, func() (kakaoClient, error) {
		if opened.Add(1) == 1 {
			return first, nil
		}
		first.mu.Lock()
		closed := first.closeCalls > 0
		first.mu.Unlock()
		if !closed {
			return nil, errors.New("replacement opened before old lease closed")
		}
		return second, nil
	})
	kc.wait = func(context.Context, time.Duration) error { return nil }
	kc.Connect(context.Background())
	close(first.stream)
	waitFor(t, func() bool { return opened.Load() == 2 && kc.IsLoggedIn() })
	close(second.stream)
	kc.Disconnect()
}

func TestDisconnectCancelsRecoveryBackoff(t *testing.T) {
	first := &fakeKakao{stream: make(chan events.Result)}
	var opened atomic.Int32
	kc, _ := newTestClient(t, func() (kakaoClient, error) {
		opened.Add(1)
		return first, nil
	})
	waitStarted := make(chan struct{})
	waitCanceled := make(chan struct{})
	kc.wait = func(ctx context.Context, _ time.Duration) error {
		close(waitStarted)
		<-ctx.Done()
		close(waitCanceled)
		return ctx.Err()
	}
	kc.Connect(context.Background())
	close(first.stream)
	select {
	case <-waitStarted:
	case <-time.After(time.Second):
		t.Fatal("recovery backoff did not start")
	}
	kc.Disconnect()
	select {
	case <-waitCanceled:
	case <-time.After(time.Second):
		t.Fatal("Disconnect did not cancel recovery backoff")
	}
	if got := opened.Load(); got != 1 {
		t.Fatalf("replacement opens = %d, want 1 total", got)
	}
}

func TestSecondRecoveryBackoffRetainsCancellationOwner(t *testing.T) {
	first := &fakeKakao{stream: make(chan events.Result)}
	failed := &fakeKakao{connectErr: io.EOF}
	var opens atomic.Int32
	kc, _ := newTestClient(t, func() (kakaoClient, error) {
		if opens.Add(1) == 1 {
			return first, nil
		}
		return failed, nil
	})
	waits := atomic.Int32{}
	secondWait := make(chan context.Context, 1)
	kc.wait = func(ctx context.Context, _ time.Duration) error {
		if waits.Add(1) == 1 {
			return nil
		}
		secondWait <- ctx
		<-ctx.Done()
		return ctx.Err()
	}
	kc.Connect(context.Background())
	close(first.stream)
	var ctx context.Context
	select {
	case ctx = <-secondWait:
	case <-time.After(time.Second):
		t.Fatal("second recovery backoff did not start")
	}
	kc.Disconnect()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("second recovery backoff lost cancellation owner")
	}
}

type closingFake struct {
	*fakeKakao
	once sync.Once
}

func (f *closingFake) Shutdown(ctx context.Context) error {
	f.once.Do(func() { close(f.stream) })
	return f.fakeKakao.Shutdown(ctx)
}

func TestExplicitConnectAfterCleanDisconnectStartsFreshGeneration(t *testing.T) {
	var opens atomic.Int32
	kc, _ := newTestClient(t, func() (kakaoClient, error) {
		opens.Add(1)
		return &closingFake{fakeKakao: &fakeKakao{stream: make(chan events.Result)}}, nil
	})
	kc.Connect(context.Background())
	kc.Disconnect()
	kc.mu.Lock()
	retained := kc.cleanup != nil
	kc.mu.Unlock()
	if retained {
		t.Fatal("clean disconnect retained a cleanup owner")
	}
	kc.Connect(context.Background())
	if got := opens.Load(); got != 2 {
		t.Fatalf("explicit reconnect opens = %d, want 2", got)
	}
	kc.Disconnect()
}

func TestCleanupTimeoutRetainsOwnerUntilLaterRetry(t *testing.T) {
	first := &fakeKakao{stream: make(chan events.Result), shutdownFailures: 1}
	var opens atomic.Int32
	kc, _ := newTestClient(t, func() (kakaoClient, error) {
		if opens.Add(1) == 1 {
			return first, nil
		}
		return nil, errors.New("replacement held for cleanup assertion")
	})
	t.Cleanup(kc.Disconnect)
	kc.wait = func(context.Context, time.Duration) error { return nil }
	kc.Connect(context.Background())
	close(first.stream)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		first.mu.Lock()
		calls := first.shutdownCalls
		first.mu.Unlock()
		kc.mu.Lock()
		free := kc.cleanup == nil
		kc.mu.Unlock()
		if calls == 2 && free {
			break
		}
		time.Sleep(time.Millisecond)
	}
	first.mu.Lock()
	calls := first.shutdownCalls
	first.mu.Unlock()
	kc.mu.Lock()
	free := kc.cleanup == nil
	kc.mu.Unlock()
	if calls != 2 || !free {
		t.Fatalf("cleanup calls=%d free=%v", calls, free)
	}
	if got := opens.Load(); got != 2 {
		t.Fatalf("replacement opens = %d after cleanup retry, want 2", got)
	}
}

func TestUnidentifiableMessageStopsRecoveryButRetriesOwnerCleanup(t *testing.T) {
	fake := &fakeKakao{stream: make(chan events.Result), shutdownFailures: 1}
	var opened atomic.Int32
	kc, harness := newTestClient(t, func() (kakaoClient, error) {
		opened.Add(1)
		return fake, nil
	})
	kc.wait = func(context.Context, time.Duration) error { return nil }
	kc.mu.Lock()
	kc.client = fake
	kc.cleanup = fake
	kc.generation = 1
	kc.mu.Unlock()
	done := make(chan struct{})
	go kc.run(fake, func() <-chan events.Result {
		stream := make(chan events.Result, 1)
		stream <- events.Result{Err: events.ErrUnidentifiableMessage}
		close(stream)
		return stream
	}(), done, 1)
	waitFor(t, func() bool {
		fake.mu.Lock()
		calls := fake.shutdownCalls
		fake.mu.Unlock()
		return calls >= 2
	})
	if opened.Load() != 0 {
		t.Fatalf("recovery opened %d replacement clients", opened.Load())
	}
	if got := harness.lastState(); got.Error != stateUnidentifiableMsg || got.StateEvent != status.StateUnknownError {
		t.Fatalf("terminal state = %#v, want stable unidentifiable-message error", got)
	}
	kc.mu.Lock()
	retained := kc.cleanup != nil
	tries := kc.schedule.attempt
	kc.mu.Unlock()
	if retained || tries != 0 {
		t.Fatalf("cleanup=%v recovery attempts=%d, want released owner and no reconnect", retained, tries)
	}
}

func TestCatchUpUnidentifiableMessageIsPermanentAdmissionFailure(t *testing.T) {
	fake := &fakeKakao{
		resumeTargets: []syncmsg.Target{{ChatID: testChatID, MaxLogID: 9}},
		catchUps:      map[int64]catchUpResult{testChatID: {err: events.ErrUnidentifiableMessage}},
	}
	kc, _ := newTestClient(t, nil)
	_, err := kc.catchUpChain(context.Background(), fake, nil)
	if !isUnidentifiableMessageError(err) {
		t.Fatalf("catch-up error = %v, want permanent unidentifiable-message error", err)
	}
	if retryableRecoveryError(bootstrapFailure{stage: "catch-up", err: err}) {
		t.Fatal("unidentifiable catch-up failure is retryable")
	}
}

func TestCleanupRetryFailureLeavesOwnerRetryableWithoutChannelReuse(t *testing.T) {
	first := &fakeKakao{stream: make(chan events.Result), shutdownFailures: 2}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return first, nil })
	kc.wait = func(context.Context, time.Duration) error { return nil }
	kc.Connect(context.Background())
	close(first.stream)
	waitFor(t, func() bool {
		first.mu.Lock()
		calls := first.shutdownCalls
		first.mu.Unlock()
		return calls >= 2
	})
	kc.Disconnect()
	kc.mu.Lock()
	retained := kc.cleanup != nil
	kc.mu.Unlock()
	if retained {
		t.Fatal("explicit cleanup did not release the retained owner")
	}
}

func TestFirstConnectTransportFailureRetries(t *testing.T) {
	connected := &fakeKakao{stream: make(chan events.Result)}
	var opens atomic.Int32
	kc, harness := newTestClient(t, func() (kakaoClient, error) {
		if opens.Add(1) == 1 {
			return &fakeKakao{connectErr: io.EOF}, nil
		}
		return connected, nil
	})
	kc.wait = func(context.Context, time.Duration) error { return nil }
	kc.Connect(context.Background())
	// The CONNECTED state follows the session install that IsLoggedIn sees.
	waitFor(t, func() bool {
		return opens.Load() == 2 && kc.IsLoggedIn() && harness.lastState().StateEvent == status.StateConnected
	})
	close(connected.stream)
	kc.Disconnect()
}

func TestFirstConnectLoginRejectionDoesNotRetry(t *testing.T) {
	var opens atomic.Int32
	kc, harness := newTestClient(t, func() (kakaoClient, error) {
		opens.Add(1)
		return &fakeKakao{connectErr: client.ErrLogin}, nil
	})
	kc.wait = func(context.Context, time.Duration) error {
		t.Error("login rejection scheduled a retry")
		return nil
	}
	kc.Connect(context.Background())
	if got := opens.Load(); got != 1 {
		t.Fatalf("opens = %d, want 1", got)
	}
	if state := harness.lastState(); state.Error != stateConnectFailed {
		t.Fatalf("final state = %+v, want %s", state, stateConnectFailed)
	}
	kc.Disconnect()
}

func TestStaleBootstrapCannotPublishConnectedState(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	fake := &fakeKakao{
		stream:         make(chan events.Result),
		resumeTargets:  []syncmsg.Target{{ChatID: testChatID, MaxLogID: 1}},
		catchupEntered: entered,
		catchupRelease: release,
	}
	kc, harness := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	done := make(chan struct{})
	go func() { kc.Connect(context.Background()); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("bootstrap did not reach catch-up")
	}
	kc.Disconnect()
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stale bootstrap did not unwind")
	}
	for _, state := range harness.stateEvents() {
		if state == status.StateConnected {
			t.Fatal("stale bootstrap published connected state")
		}
	}
}

func TestKickoutIsPersistedAndBlocksReconnectAfterRestart(t *testing.T) {
	ctx := context.Background()
	first := &fakeKakao{stream: make(chan events.Result, 1)}
	var opens atomic.Int32
	kc, harness, br := newFrameworkTestClient(t, func() (kakaoClient, error) {
		opens.Add(1)
		return first, nil
	})
	user := kc.login.User
	record := kc.login.UserLogin
	kc.wait = func(context.Context, time.Duration) error { return nil }
	kc.Connect(ctx)
	first.stream <- events.Result{Event: events.Kickout{Reason: 7}}
	close(first.stream)
	waitFor(t, func() bool { return harness.lastState().StateEvent == status.StateBadCredentials })
	if state := harness.lastState(); state.Error != stateKickedOut || !strings.Contains(state.Message, "reason 7") {
		t.Fatalf("kickout state = %+v, want %s naming reason 7", state, stateKickedOut)
	}
	kc.Disconnect()
	if got := opens.Load(); got != 1 {
		t.Fatalf("opens after kickout = %d, want 1", got)
	}

	// A restart loads the saved login. It must not open the profile again.
	reloaded, err := br.DB.UserLogin.GetByID(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved, ok := reloaded.Metadata.(*UserLoginMetadata)
	if !ok || saved.KickedOut == nil || saved.KickedOut.Reason != 7 {
		t.Fatalf("saved metadata = %+v, want kickout reason 7", reloaded.Metadata)
	}
	restartedLogin := &bridgev2.UserLogin{UserLogin: reloaded, Bridge: br, User: user, Log: zerolog.Nop()}
	restarted := newKakaoClient(restartedLogin, newBridgeStore(br.DB), testSelfID, func() (kakaoClient, error) {
		t.Error("restarted bridge opened a kicked-out profile")
		return nil, errors.New("unexpected open")
	})
	restartedHarness := &testHarness{}
	restarted.sendState = restartedHarness.sendState
	restarted.Connect(ctx)
	if state := restartedHarness.lastState(); state.StateEvent != status.StateBadCredentials || state.Error != stateKickedOut {
		t.Fatalf("restarted state = %+v, want BAD_CREDENTIALS %s", state, stateKickedOut)
	}
	if restarted.IsLoggedIn() {
		t.Fatal("restarted kicked-out login reports logged in")
	}
	restarted.Disconnect()
}

func TestChangeServerCleansUpAndReopens(t *testing.T) {
	first := &fakeKakao{stream: make(chan events.Result, 1)}
	second := &fakeKakao{stream: make(chan events.Result)}
	var opens atomic.Int32
	kc, _ := newTestClient(t, func() (kakaoClient, error) {
		if opens.Add(1) == 1 {
			return first, nil
		}
		first.mu.Lock()
		closed := first.closeCalls > 0
		first.mu.Unlock()
		if !closed {
			return nil, errors.New("change-server replacement opened before cleanup")
		}
		return second, nil
	})
	kc.wait = func(context.Context, time.Duration) error { return nil }
	kc.Connect(context.Background())
	first.stream <- events.Result{Event: events.ChangeServer{}}
	close(first.stream)
	waitFor(t, func() bool { return opens.Load() == 2 && kc.IsLoggedIn() })
	close(second.stream)
	kc.Disconnect()
}

func TestRecoveryPolicyRejectsTerminalAndUnknownFailures(t *testing.T) {
	if retryableRecoveryError(client.ErrLogin) {
		t.Fatal("login rejection must not retry")
	}
	if retryableRecoveryError(client.ErrCredentialRenewal) {
		t.Fatal("credential renewal failure must not retry")
	}
	if retryableRecoveryError(client.StatusError{Command: "LOGINLIST", Status: -950}) {
		t.Fatal("second -950 must not retry")
	}
	if retryableRecoveryError(client.StatusError{Command: "LOGINLIST", Status: -321}) {
		t.Fatal("unknown LOGINLIST status must not retry")
	}
	if !retryableRecoveryError(client.StatusError{Command: "LOGINLIST", Status: -328}) {
		t.Fatal("-328 must retry with bounded delay")
	}
	for _, err := range []error{errors.New("catch up chat: delivery failed"), client.ErrProtocol, errors.New("matrix conversion failed")} {
		if retryableRecoveryError(err) {
			t.Fatalf("non-transport bootstrap failure %v must not retry", err)
		}
	}
	if !retryableRecoveryError(io.EOF) || !retryableRecoveryError(client.ErrClosed) {
		t.Fatal("transport/session closure must retry")
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}

func TestUndecodableMembershipStopsRecoveryButReleasesOwner(t *testing.T) {
	fake := &fakeKakao{stream: make(chan events.Result), shutdownFailures: 1}
	var opened atomic.Int32
	kc, harness := newTestClient(t, func() (kakaoClient, error) {
		opened.Add(1)
		return fake, nil
	})
	kc.wait = func(context.Context, time.Duration) error { return nil }
	kc.mu.Lock()
	kc.client = fake
	kc.cleanup = fake
	kc.generation = 1
	kc.mu.Unlock()
	done := make(chan struct{})
	go kc.run(fake, func() <-chan events.Result {
		stream := make(chan events.Result, 1)
		stream <- events.Result{Err: events.ErrUnidentifiableMembership}
		close(stream)
		return stream
	}(), done, 1)
	waitFor(t, func() bool {
		fake.mu.Lock()
		calls := fake.shutdownCalls
		fake.mu.Unlock()
		return calls >= 2
	})
	if opened.Load() != 0 {
		t.Fatalf("recovery opened %d replacement clients", opened.Load())
	}
	if got := harness.lastState(); got.Error != stateGroupMembershipInvalid || got.StateEvent != status.StateUnknownError {
		t.Fatalf("terminal state = %#v, want stable membership error", got)
	}
	kc.mu.Lock()
	retained := kc.cleanup != nil
	tries := kc.schedule.attempt
	kc.mu.Unlock()
	if retained || tries != 0 {
		t.Fatalf("cleanup=%v recovery attempts=%d, want released owner and no reconnect", retained, tries)
	}
}

func TestRemovalBootstrapFailureRetainsActionableState(t *testing.T) {
	fake := &fakeKakao{connectErr: errors.Join(errSourceAccessRemoved, errMembershipPending)}
	kc, harness := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	kc.Connect(context.Background())
	if got := harness.lastState(); got.Error != stateGroupAccessRemoved || got.StateEvent != status.StateUnknownError {
		t.Fatalf("removal bootstrap state = %#v", got)
	}
	kc.Disconnect()
}

func TestConnectedTransportDoesNotHideRemovedGroupState(t *testing.T) {
	fake := &fakeKakao{stream: make(chan events.Result)}
	kc, harness := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	kc.sourceBlocked = map[int64]error{testChatID: errSourceAccessRemoved}
	kc.Connect(context.Background())
	if got := harness.lastState(); got.Error != stateGroupAccessRemoved || got.StateEvent != status.StateUnknownError {
		t.Fatalf("connected transport hid removed group: %#v", got)
	}
	kc.Disconnect()
}
