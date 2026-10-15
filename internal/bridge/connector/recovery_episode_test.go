package connector

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2/status"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/events"
)

// steppedClock is a test clock that advances only when the client waits out
// a recovery delay, or when the test moves it.
type steppedClock struct {
	mu     sync.Mutex
	now    time.Time
	delays []time.Duration
}

func newSteppedClock() *steppedClock {
	return &steppedClock{now: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)}
}

func (c *steppedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *steppedClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *steppedClock) Wait(_ context.Context, delay time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.delays = append(c.delays, delay)
	c.now = c.now.Add(delay)
	return nil
}

func (c *steppedClock) Delays() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.delays...)
}

// watchdogTimers records every armed episode watchdog so a test can fire it.
type watchdogTimers struct {
	mu     sync.Mutex
	timers []*watchdogTimer
}

type watchdogTimer struct {
	delay   time.Duration
	fire    func()
	stopped atomic.Bool
}

func (t *watchdogTimer) Stop() bool { return !t.stopped.Swap(true) }

func (w *watchdogTimers) after(delay time.Duration, fire func()) stopper {
	w.mu.Lock()
	defer w.mu.Unlock()
	timer := &watchdogTimer{delay: delay, fire: fire}
	w.timers = append(w.timers, timer)
	return timer
}

func (w *watchdogTimers) all() []*watchdogTimer {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]*watchdogTimer(nil), w.timers...)
}

func repeatDelay(delay time.Duration, n int) []time.Duration {
	delays := make([]time.Duration, n)
	for i := range delays {
		delays[i] = delay
	}
	return delays
}

func assertDelays(t *testing.T, got, want []time.Duration) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("recovery delays = %v (%d), want %v (%d)", got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("recovery delay %d = %s, want %s; all delays %v", i, got[i], want[i], got)
		}
	}
}

func countStoppedStates(t *testing.T, harness *testHarness) int {
	t.Helper()
	stops := 0
	for _, state := range harness.allStates() {
		if state.Error == stateRecoveryStopped {
			stops++
			if state.StateEvent != status.StateUnknownError || state.Message == "" {
				t.Fatalf("stopped state = %+v, want UNKNOWN_ERROR with a message", state)
			}
		}
	}
	return stops
}

// The native episode: the first attempt after a session loss runs after
// 0.1 s and every failed attempt, whatever the LOGINLIST status, retries
// after 10 s. No attempt is scheduled at or after the 600 s watchdog, and the
// watchdog stops recovery until the operator reconnects.
func TestRecoveryEpisodeRetriesEveryTenSecondsThenStops(t *testing.T) {
	clock := newSteppedClock()
	timers := &watchdogTimers{}
	first := &fakeKakao{stream: make(chan events.Result)}
	recovered := &fakeKakao{stream: make(chan events.Result)}
	failures := []error{
		io.EOF,
		client.StatusError{Command: "LOGINLIST", Status: -328},
		client.StatusError{Command: "LOGINLIST", Status: -310},
		client.ErrLogin,
	}
	var opens atomic.Int32
	kc, harness := newTestClient(t, func() (kakaoClient, error) {
		switch n := opens.Add(1); {
		case n == 1:
			return first, nil
		case n <= 1+60:
			return &fakeKakao{connectErr: failures[int(n)%len(failures)]}, nil
		default:
			return recovered, nil
		}
	})
	kc.now = clock.Now
	kc.wait = clock.Wait
	kc.watchdogAfter = timers.after
	kc.Connect(context.Background())
	if armed := timers.all(); len(armed) != 1 || !armed[0].stopped.Load() {
		t.Fatalf("first connect watchdogs = %d, want one, stopped by the login", len(armed))
	}
	clock.Advance(time.Hour)
	close(first.stream)
	// Attempt k runs 10(k-1) s into the episode: attempts 1..59 retry, and
	// attempt 60's retry would run at 600 s, the watchdog.
	waitFor(t, func() bool { return opens.Load() == 61 && len(clock.Delays()) == 60 })
	time.Sleep(50 * time.Millisecond)
	assertDelays(t, clock.Delays(), append([]time.Duration{firstRecoveryDelay}, repeatDelay(episodeRetryDelay, 59)...))
	armed := timers.all()
	if len(armed) != 2 || armed[1].delay != episodeWatchdog || armed[1].stopped.Load() {
		t.Fatalf("episode watchdogs = %d, want a second, armed for %s", len(armed), episodeWatchdog)
	}

	armed[1].fire()
	if got := countStoppedStates(t, harness); got != 1 {
		t.Fatalf("stopped states = %d, want 1", got)
	}
	if kc.IsLoggedIn() || opens.Load() != 61 {
		t.Fatalf("logged in=%v opens=%d after the watchdog, want stopped at 61", kc.IsLoggedIn(), opens.Load())
	}

	// The operator's reconnect command starts a fresh episode.
	if got := kc.operatorReconnect(); got != reconnectStarted {
		t.Fatalf("reconnect after the watchdog = %v, want started", got)
	}
	waitFor(t, func() bool { return opens.Load() == 62 && kc.IsLoggedIn() })
	close(recovered.stream)
	kc.Disconnect()
}

// A completed login ends the episode: the next session loss starts a new
// one at 0.1 s with its own watchdog.
func TestRecoveryEpisodeResetsAfterCompletedLogin(t *testing.T) {
	clock := newSteppedClock()
	timers := &watchdogTimers{}
	sessions := []*fakeKakao{
		{stream: make(chan events.Result)},
		{stream: make(chan events.Result)},
		{stream: make(chan events.Result)},
	}
	var opens atomic.Int32
	kc, _ := newTestClient(t, func() (kakaoClient, error) {
		switch n := opens.Add(1); n {
		case 1:
			return sessions[0], nil
		case 2, 3, 5:
			return &fakeKakao{connectErr: io.EOF}, nil
		case 4:
			return sessions[1], nil
		default:
			return sessions[2], nil
		}
	})
	kc.now = clock.Now
	kc.wait = clock.Wait
	kc.watchdogAfter = timers.after
	kc.Connect(context.Background())
	close(sessions[0].stream)
	waitFor(t, func() bool { return opens.Load() == 4 && kc.IsLoggedIn() })
	close(sessions[1].stream)
	waitFor(t, func() bool { return opens.Load() == 6 && kc.IsLoggedIn() })

	assertDelays(t, clock.Delays(), []time.Duration{firstRecoveryDelay, episodeRetryDelay, episodeRetryDelay, firstRecoveryDelay, episodeRetryDelay})
	armed := timers.all()
	if len(armed) != 3 {
		t.Fatalf("watchdogs = %d, want one per episode (3)", len(armed))
	}
	for i, timer := range armed {
		if !timer.stopped.Load() {
			t.Fatalf("watchdog %d still armed after its episode's login completed", i)
		}
	}
	close(sessions[2].stream)
	kc.Disconnect()
}

// A failed first connect is the episode's first attempt.
func TestFirstConnectFailureRetriesAtEpisodeCadence(t *testing.T) {
	clock := newSteppedClock()
	connected := &fakeKakao{stream: make(chan events.Result)}
	var opens atomic.Int32
	kc, _ := newTestClient(t, func() (kakaoClient, error) {
		if opens.Add(1) <= 2 {
			return &fakeKakao{connectErr: io.EOF}, nil
		}
		return connected, nil
	})
	kc.now = clock.Now
	kc.wait = clock.Wait
	kc.watchdogAfter = (&watchdogTimers{}).after
	kc.Connect(context.Background())
	waitFor(t, func() bool { return opens.Load() == 3 && kc.IsLoggedIn() })
	assertDelays(t, clock.Delays(), []time.Duration{episodeRetryDelay, episodeRetryDelay})
	close(connected.stream)
	kc.Disconnect()
}

// A delta-only LOGINLIST (-305) connects but never completes the episode,
// as in the official client, so the watchdog still stops the session.
func TestDeltaOnlyLoginStopsAtWatchdog(t *testing.T) {
	timers := &watchdogTimers{}
	session := &fakeKakao{stream: make(chan events.Result), deltaOnly: true}
	kc, harness := newTestClient(t, func() (kakaoClient, error) { return session, nil })
	kc.watchdogAfter = timers.after
	kc.Connect(context.Background())
	if !kc.IsLoggedIn() {
		t.Fatal("delta-only login did not connect")
	}
	armed := timers.all()
	if len(armed) != 1 || armed[0].stopped.Load() {
		t.Fatalf("watchdogs = %d, want one still armed after a delta-only login", len(armed))
	}
	armed[0].fire()
	if kc.IsLoggedIn() {
		t.Fatal("delta-only session survived the watchdog")
	}
	if got := countStoppedStates(t, harness); got != 1 {
		t.Fatalf("stopped states = %d, want 1", got)
	}
}

// LOGINLIST -445 and a renewal that KakaoTalk ends are the native logouts:
// no retry, and the episode ends at once.
func TestTerminalLoginFailureEndsEpisode(t *testing.T) {
	for _, err := range []error{
		client.StatusError{Command: "LOGINLIST", Status: -445},
		client.ErrSignedOut,
	} {
		timers := &watchdogTimers{}
		var opens atomic.Int32
		kc, _ := newTestClient(t, func() (kakaoClient, error) {
			opens.Add(1)
			return &fakeKakao{connectErr: err}, nil
		})
		kc.wait = func(context.Context, time.Duration) error {
			t.Errorf("%v scheduled a retry", err)
			return nil
		}
		kc.watchdogAfter = timers.after
		kc.Connect(context.Background())
		kc.mu.Lock()
		active := kc.schedule.active()
		kc.mu.Unlock()
		if opens.Load() != 1 || active {
			t.Fatalf("%v: opens=%d episode active=%v, want one attempt and no episode", err, opens.Load(), active)
		}
		if armed := timers.all(); len(armed) != 1 || !armed[0].stopped.Load() {
			t.Fatalf("%v: watchdog not stopped", err)
		}
		kc.Disconnect()
	}
}

// blockingConnect is a client whose Connect waits until its attempt is
// cancelled, like a CHECKIN walk during an outage.
type blockingConnect struct {
	*fakeKakao
	entered chan struct{}
}

func (b *blockingConnect) Connect(ctx context.Context) error {
	close(b.entered)
	<-ctx.Done()
	return ctx.Err()
}

// Regression (lab, 2026-10-15): the watchdog stopped recovery while an
// attempt was in flight, the stopped attempt left the client marked as
// connecting, and the reconnect command then reported a reconnect already in
// progress forever.
func TestWatchdogDuringAttemptLetsOperatorReconnect(t *testing.T) {
	clock := newSteppedClock()
	timers := &watchdogTimers{}
	first := &fakeKakao{stream: make(chan events.Result)}
	blocked := &blockingConnect{fakeKakao: &fakeKakao{}, entered: make(chan struct{})}
	recovered := &fakeKakao{stream: make(chan events.Result)}
	var opens atomic.Int32
	kc, harness := newTestClient(t, func() (kakaoClient, error) {
		switch opens.Add(1) {
		case 1:
			return first, nil
		case 2:
			return blocked, nil
		default:
			return recovered, nil
		}
	})
	kc.now = clock.Now
	kc.wait = clock.Wait
	kc.watchdogAfter = timers.after
	kc.Connect(context.Background())
	close(first.stream)
	<-blocked.entered
	armed := timers.all()
	if len(armed) != 2 {
		t.Fatalf("watchdogs = %d, want 2", len(armed))
	}
	armed[1].fire()
	if got := countStoppedStates(t, harness); got != 1 {
		t.Fatalf("stopped states = %d, want 1", got)
	}
	if got := kc.operatorReconnect(); got != reconnectStarted {
		t.Fatalf("reconnect after a watchdog during an attempt = %v, want started", got)
	}
	waitFor(t, func() bool { return opens.Load() == 3 && kc.IsLoggedIn() })
	close(recovered.stream)
	kc.Disconnect()
}
