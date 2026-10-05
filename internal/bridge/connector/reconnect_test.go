package connector

import (
	"context"
	"errors"
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
