package connector

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/events"
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

func TestRecoveryAttemptBudgetIsBounded(t *testing.T) {
	if len(ordinaryRecoveryDelays) != maxRecoveryAttempts || len(rateLimitedRecoveryDelays) != maxRecoveryAttempts {
		t.Fatalf("recovery policy lengths = %d/%d, want %d", len(ordinaryRecoveryDelays), len(rateLimitedRecoveryDelays), maxRecoveryAttempts)
	}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return nil, errors.New("unused") })
	kc.recoveryTry = maxRecoveryAttempts
	if kc.retryAfter(client.ErrClosed, kc.generation) {
		t.Fatal("recovery scheduled past the bounded attempt budget")
	}
}

func TestRecoveryPolicyRejectsTerminalAndUnknownFailures(t *testing.T) {
	for i, want := range ordinaryRecoveryDelays {
		if got := recoveryDelay(errors.New("transport closed"), i); got != want {
			t.Fatalf("ordinary recovery delay %d = %s, want %s", i, got, want)
		}
	}
	for i, want := range rateLimitedRecoveryDelays {
		if got := recoveryDelay(client.StatusError{Command: "LOGINLIST", Status: -328}, i); got != want {
			t.Fatalf("-328 recovery delay %d = %s, want %s", i, got, want)
		}
	}
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
