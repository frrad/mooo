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

func countStalledStates(t *testing.T, harness *testHarness) int {
	t.Helper()
	stalls := 0
	for _, state := range harness.allStates() {
		if state.Error == stateRecoveryStalled {
			stalls++
			if state.StateEvent != status.StateUnknownError || state.Message == "" {
				t.Fatalf("stalled state = %+v, want UNKNOWN_ERROR with a message", state)
			}
		}
	}
	return stalls
}

// The native episode: the first attempt after a session loss runs after
// 0.1 s, failed attempts retry every 10 s, and no attempt runs at or past
// 600 s after the episode's first attempt. The bridge then reports a stall
// once and keeps retrying every 15 minutes instead of logging out.
func TestRecoveryEpisodeRetriesEveryTenSecondsUntilWatchdog(t *testing.T) {
	clock := newSteppedClock()
	first := &fakeKakao{stream: make(chan events.Result)}
	recovered := &fakeKakao{stream: make(chan events.Result)}
	var opens atomic.Int32
	kc, harness := newTestClient(t, func() (kakaoClient, error) {
		switch n := opens.Add(1); {
		case n == 1:
			return first, nil
		case n <= 1+62:
			return &fakeKakao{connectErr: io.EOF}, nil
		default:
			return recovered, nil
		}
	})
	kc.now = clock.Now
	kc.wait = clock.Wait
	kc.Connect(context.Background())
	clock.Advance(time.Hour)
	close(first.stream)
	waitFor(t, func() bool { return opens.Load() == 64 && kc.IsLoggedIn() })

	// 62 failed attempts. Attempt k runs 10(k-1) s into the episode, so
	// attempts 1..59 wait 10 s. Attempt 60 would retry at 600 s, the
	// watchdog, so attempts 60..62 wait 15 minutes.
	want := append([]time.Duration{firstRecoveryDelay}, repeatDelay(10*time.Second, 59)...)
	want = append(want, 15*time.Minute, 15*time.Minute, 15*time.Minute)
	assertDelays(t, clock.Delays(), want)
	if stalls := countStalledStates(t, harness); stalls != 1 {
		t.Fatalf("stalled states = %d, want exactly 1 when the watchdog passes", stalls)
	}
	if harness.lastState().StateEvent != status.StateConnected {
		t.Fatalf("final state = %+v, want CONNECTED", harness.lastState())
	}
	close(recovered.stream)
	kc.Disconnect()
}

// A successful login ends the episode: the next session loss starts again at
// 0.1 s and 10 s, even after the previous episode reached the slow tier.
func TestRecoveryEpisodeResetsAfterSuccessfulLogin(t *testing.T) {
	clock := newSteppedClock()
	sessions := []*fakeKakao{
		{stream: make(chan events.Result)},
		{stream: make(chan events.Result)},
		{stream: make(chan events.Result)},
	}
	// Session 1 ends; 61 failures cross the watchdog; session 2 connects and
	// ends; one failure; session 3 connects.
	var opens atomic.Int32
	kc, _ := newTestClient(t, func() (kakaoClient, error) {
		switch n := opens.Add(1); {
		case n == 1:
			return sessions[0], nil
		case n <= 1+61:
			return &fakeKakao{connectErr: io.EOF}, nil
		case n == 63:
			return sessions[1], nil
		case n == 64:
			return &fakeKakao{connectErr: io.EOF}, nil
		default:
			return sessions[2], nil
		}
	})
	kc.now = clock.Now
	kc.wait = clock.Wait
	kc.Connect(context.Background())
	clock.Advance(time.Hour)
	close(sessions[0].stream)
	waitFor(t, func() bool { return opens.Load() == 63 && kc.IsLoggedIn() })
	clock.Advance(time.Hour)
	close(sessions[1].stream)
	waitFor(t, func() bool { return opens.Load() == 65 && kc.IsLoggedIn() })

	want := append([]time.Duration{firstRecoveryDelay}, repeatDelay(10*time.Second, 59)...)
	want = append(want, 15*time.Minute, 15*time.Minute)
	want = append(want, firstRecoveryDelay, 10*time.Second)
	assertDelays(t, clock.Delays(), want)
	close(sessions[2].stream)
	kc.Disconnect()
}

// A failed first connect is the episode's first attempt and retries at the
// episode cadence.
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
	kc.Connect(context.Background())
	waitFor(t, func() bool { return opens.Load() == 3 && kc.IsLoggedIn() })
	assertDelays(t, clock.Delays(), []time.Duration{10 * time.Second, 10 * time.Second})
	close(connected.stream)
	kc.Disconnect()
}

// Sessions that end within a minute of their login, more than three times
// in a row, reconnect on the slow tier. A session that lasts longer resets
// the count.
func TestShortSessionsJoinSlowTier(t *testing.T) {
	clock := newSteppedClock()
	minute := &fakeKakao{stream: make(chan events.Result)}
	last := &fakeKakao{stream: make(chan events.Result)}
	var opens atomic.Int32
	kc, harness := newTestClient(t, func() (kakaoClient, error) {
		switch n := opens.Add(1); {
		case n <= 5:
			stream := make(chan events.Result, 1)
			stream <- events.Result{Event: events.ChangeServer{}}
			close(stream)
			return &fakeKakao{stream: stream}, nil
		case n == 6:
			return minute, nil
		default:
			return last, nil
		}
	})
	kc.now = clock.Now
	kc.wait = clock.Wait
	kc.Connect(context.Background())
	waitFor(t, func() bool { return opens.Load() == 6 && kc.IsLoggedIn() })
	assertDelays(t, clock.Delays(), []time.Duration{firstRecoveryDelay, firstRecoveryDelay, firstRecoveryDelay, 15 * time.Minute, 15 * time.Minute})
	if stalls := countStalledStates(t, harness); stalls != 1 {
		t.Fatalf("stalled states = %d, want 1 when short sessions join the slow tier", stalls)
	}
	// This session lasts a minute; its end starts a normal episode.
	clock.Advance(shortSessionDuration)
	close(minute.stream)
	waitFor(t, func() bool { return opens.Load() == 7 && kc.IsLoggedIn() })
	if got := clock.Delays(); len(got) != 6 || got[5] != firstRecoveryDelay {
		t.Fatalf("delays = %v, want a last delay of %s after a session of a minute", got, firstRecoveryDelay)
	}
	close(last.stream)
	kc.Disconnect()
}

// -328 keeps its own ladder, never the 10 s cadence, and an ordinary failure
// in the same episode returns to the episode schedule.
func TestRateLimitedRecoveryKeepsItsLadder(t *testing.T) {
	clock := newSteppedClock()
	limited := client.StatusError{Command: "LOGINLIST", Status: -328}
	failures := []error{io.EOF, limited, limited, io.EOF, limited, limited, limited, limited, io.EOF}
	connected := &fakeKakao{stream: make(chan events.Result)}
	var opens atomic.Int32
	kc, harness := newTestClient(t, func() (kakaoClient, error) {
		n := int(opens.Add(1))
		if n <= len(failures) {
			return &fakeKakao{connectErr: failures[n-1]}, nil
		}
		return connected, nil
	})
	kc.now = clock.Now
	kc.wait = clock.Wait
	kc.Connect(context.Background())
	waitFor(t, func() bool { return int(opens.Load()) == len(failures)+1 && kc.IsLoggedIn() })
	assertDelays(t, clock.Delays(), []time.Duration{
		10 * time.Second,
		60 * time.Second, 120 * time.Second,
		10 * time.Second,
		240 * time.Second, 480 * time.Second, 900 * time.Second, 15 * time.Minute,
		// The episode began 1820 s ago, past the watchdog.
		15 * time.Minute,
	})
	if stalls := countStalledStates(t, harness); stalls != 1 {
		t.Fatalf("stalled states = %d, want 1", stalls)
	}
	close(connected.stream)
	kc.Disconnect()
}

// A restart in the middle of an episode continues it: the -328 ladder and
// the watchdog keep counting from the saved state.
func TestRestartContinuesSavedEpisode(t *testing.T) {
	ctx := context.Background()
	clock := newSteppedClock()
	limited := client.StatusError{Command: "LOGINLIST", Status: -328}
	first, _, br := newFrameworkTestClient(t, func() (kakaoClient, error) {
		return &fakeKakao{connectErr: limited}, nil
	})
	first.now = clock.Now
	scheduled := make(chan time.Duration, 3)
	first.wait = func(ctx context.Context, delay time.Duration) error {
		scheduled <- delay
		if len(scheduled) < 2 {
			clock.Advance(delay)
			return nil
		}
		<-ctx.Done()
		return ctx.Err()
	}
	first.Connect(ctx)
	waitFor(t, func() bool { return len(scheduled) == 2 })
	first.Disconnect()

	var opens atomic.Int32
	restarted, _ := restartFrameworkClient(t, br, first, func() (kakaoClient, error) {
		if opens.Add(1) == 1 {
			return &fakeKakao{connectErr: limited}, nil
		}
		return &closingFake{fakeKakao: &fakeKakao{stream: make(chan events.Result)}}, nil
	})
	// The process restarts after the saved 120 s retry came due.
	clock.Advance(121 * time.Second)
	restarted.now = clock.Now
	restarted.wait = clock.Wait
	restarted.Connect(ctx)
	waitFor(t, func() bool { return opens.Load() == 2 && restarted.IsLoggedIn() })
	if got := clock.Delays(); len(got) != 1 || got[0] != 240*time.Second {
		t.Fatalf("delays after restart = %v, want [4m0s]: the third -328 of the saved episode", got)
	}
	restarted.Disconnect()
}
