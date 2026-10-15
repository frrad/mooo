package connector

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/status"

	"github.com/frrad/mooo/internal/protocol/events"
)

// restartFrameworkClient loads the previous client's login from the bridge
// database, as the framework does when the process starts again.
func restartFrameworkClient(t *testing.T, br *bridgev2.Bridge, previous *KakaoClient, open func() (kakaoClient, error)) (*KakaoClient, *testHarness) {
	t.Helper()
	reloaded, err := br.DB.UserLogin.GetByID(context.Background(), previous.login.ID)
	if err != nil {
		t.Fatal(err)
	}
	login := &bridgev2.UserLogin{UserLogin: reloaded, Bridge: br, User: previous.login.User, Log: zerolog.Nop()}
	kc := newKakaoClient(login, newBridgeStore(br.DB), testSelfID, open)
	harness := &testHarness{}
	kc.queue = harness.queue
	kc.sendState = harness.sendState
	return kc, harness
}

// recordingWait records each delay and returns at once. It fails the test if
// the profile was opened before the first delay.
type recordingWait struct {
	t      *testing.T
	opens  *atomic.Int32
	mu     sync.Mutex
	delays []time.Duration
}

func (w *recordingWait) wait(_ context.Context, delay time.Duration) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.delays) == 0 && w.opens.Load() != 0 {
		w.t.Error("restarted bridge opened the profile before waiting")
	}
	w.delays = append(w.delays, delay)
	return nil
}

func (w *recordingWait) got() []time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]time.Duration(nil), w.delays...)
}

func TestRestartWaitsOutPersistedRecoveryBackoff(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	first, _, br := newFrameworkTestClient(t, func() (kakaoClient, error) {
		return &fakeKakao{connectErr: io.EOF}, nil
	})
	first.now = func() time.Time { return start }
	scheduled := make(chan time.Duration, 1)
	first.wait = func(ctx context.Context, delay time.Duration) error {
		scheduled <- delay
		<-ctx.Done()
		return ctx.Err()
	}
	first.Connect(ctx)
	select {
	case delay := <-scheduled:
		if delay != 10*time.Second {
			t.Fatalf("first recovery delay = %s, want 10s", delay)
		}
	case <-time.After(time.Second):
		t.Fatal("failed first connect did not schedule a retry")
	}
	// The process stops during the backoff.
	first.Disconnect()

	var opens atomic.Int32
	connected := &closingFake{fakeKakao: &fakeKakao{stream: make(chan events.Result)}}
	restarted, harness := restartFrameworkClient(t, br, first, func() (kakaoClient, error) {
		opens.Add(1)
		return connected, nil
	})
	restarted.now = func() time.Time { return start.Add(2 * time.Second) }
	waits := &recordingWait{t: t, opens: &opens}
	restarted.wait = waits.wait
	restarted.Connect(ctx)
	waitFor(t, restarted.IsLoggedIn)

	if got := waits.got(); len(got) != 1 || got[0] != 8*time.Second {
		t.Fatalf("restart delays = %v, want [8s]: the rest of the saved 10s backoff", got)
	}
	scheduledStates := 0
	for _, state := range harness.allStates() {
		if state.Error == stateReconnectScheduled {
			scheduledStates++
			if state.StateEvent != status.StateTransientDisconnect || state.Message == "" {
				t.Fatalf("scheduled state = %+v, want TRANSIENT_DISCONNECT with a message", state)
			}
		}
	}
	if scheduledStates != 1 {
		t.Fatalf("scheduled states = %d, want 1", scheduledStates)
	}
	restarted.Disconnect()
}

func TestRestartsSoonAfterLoginEscalateBackoff(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	open := func() (kakaoClient, error) {
		return &closingFake{fakeKakao: &fakeKakao{stream: make(chan events.Result)}}, nil
	}
	previous, _, br := newFrameworkTestClient(t, open)
	previous.now = func() time.Time { return start }
	previous.Connect(ctx)
	waitFor(t, previous.IsLoggedIn)
	// The process dies shortly after every login, as in a crash loop under a
	// supervisor that restarts it at once.
	previous.Disconnect()

	// The first three restarts count as short sessions and log in at once;
	// later ones wait out the slow tier from the previous login.
	loginAt := start
	want := []time.Duration{0, 0, 0, 15 * time.Minute, 15 * time.Minute}
	for i, backoff := range want {
		restartAt := loginAt.Add(time.Second)
		var opens atomic.Int32
		restarted, _ := restartFrameworkClient(t, br, previous, func() (kakaoClient, error) {
			opens.Add(1)
			return open()
		})
		var clock atomic.Int64
		clock.Store(restartAt.UnixNano())
		restarted.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
		waits := &recordingWait{t: t, opens: &opens}
		restarted.wait = func(ctx context.Context, delay time.Duration) error {
			err := waits.wait(ctx, delay)
			clock.Add(int64(delay))
			return err
		}
		restarted.Connect(ctx)
		waitFor(t, restarted.IsLoggedIn)
		got := waits.got()
		switch {
		case backoff == 0 && len(got) != 0:
			t.Fatalf("restart %d delays = %v, want none", i+1, got)
		case backoff != 0 && (len(got) != 1 || got[0] != backoff-time.Second):
			t.Fatalf("restart %d delays = %v, want [%s]", i+1, got, backoff-time.Second)
		}
		loginAt = time.Unix(0, clock.Load()).UTC()
		restarted.Disconnect()
		previous = restarted
	}
}

func TestRestartAfterStableSessionConnectsAtOnce(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	open := func() (kakaoClient, error) {
		return &closingFake{fakeKakao: &fakeKakao{stream: make(chan events.Result)}}, nil
	}
	previous, _, br := newFrameworkTestClient(t, open)
	previous.now = func() time.Time { return start }
	previous.Connect(ctx)
	waitFor(t, previous.IsLoggedIn)
	previous.Disconnect()

	restarted, _ := restartFrameworkClient(t, br, previous, open)
	restarted.now = func() time.Time { return start.Add(shortSessionDuration) }
	restarted.wait = func(context.Context, time.Duration) error {
		t.Error("restart after a stable session waited")
		return nil
	}
	restarted.Connect(ctx)
	if !restarted.IsLoggedIn() {
		t.Fatal("restart after a stable session did not connect at once")
	}
	restarted.Disconnect()
}

func TestRestartOfNewLoginConnectsAtOnce(t *testing.T) {
	kc, _, _ := newFrameworkTestClient(t, func() (kakaoClient, error) {
		return &closingFake{fakeKakao: &fakeKakao{stream: make(chan events.Result)}}, nil
	})
	kc.wait = func(context.Context, time.Duration) error {
		t.Error("login without a saved schedule waited")
		return nil
	}
	kc.Connect(context.Background())
	if !kc.IsLoggedIn() {
		t.Fatal("login without a saved schedule did not connect at once")
	}
	kc.Disconnect()
}
