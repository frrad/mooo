package sessionlogin

import (
	"testing"
	"time"
)

type inSegmentQueue struct{ work []func() }

func (q *inSegmentQueue) Enqueue(fn func()) { q.work = append(q.work, fn) }
func (q *inSegmentQueue) run() {
	work := q.work
	q.work = nil
	for _, fn := range work {
		fn()
	}
}

type inSegmentConfig struct{ timeout time.Duration }

func (c *inSegmentConfig) InSegmentTimeout() time.Duration { return c.timeout }

type inSegmentTimerFake struct {
	fn      func()
	stopped bool
}

func (t *inSegmentTimerFake) Stop() bool { was := !t.stopped; t.stopped = true; return was }

type inSegmentClock struct {
	timers []*inSegmentTimerFake
	delays []time.Duration
}

func (c *inSegmentClock) AfterFunc(delay time.Duration, fn func()) InSegmentTimeoutTimer {
	t := &inSegmentTimerFake{fn: fn}
	c.timers = append(c.timers, t)
	c.delays = append(c.delays, delay)
	return t
}

func TestInSegmentTimeoutOwnerAdmissionAndExecution(t *testing.T) {
	q, clock, cfg := &inSegmentQueue{}, &inSegmentClock{}, &inSegmentConfig{timeout: 5 * time.Second}
	fired := 0
	owner, err := NewInSegmentTimeoutOwner(clock, q, cfg, func() { fired++ })
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := owner.Toggle(1); err != nil || !ok {
		t.Fatalf("enable = %v, %v", ok, err)
	}
	if len(q.work) != 1 {
		t.Fatalf("queued=%d, want 1", len(q.work))
	}
	cfg.timeout = 2500 * time.Millisecond
	q.run()
	if len(clock.delays) != 1 || clock.delays[0] != cfg.timeout {
		t.Fatalf("delay=%v, want %v", clock.delays, cfg.timeout)
	}
	clock.timers[0].fn()
	clock.timers[0].fn()
	if fired != 1 {
		t.Fatalf("fired=%d, want one-shot", fired)
	}
}

func TestInSegmentTimeoutOwnerDisableAndAdmissionGate(t *testing.T) {
	q, clock, cfg := &inSegmentQueue{}, &inSegmentClock{}, &inSegmentConfig{timeout: time.Second}
	owner, err := NewInSegmentTimeoutOwner(clock, q, cfg, func() { t.Fatal("disconnect after disable") })
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := owner.Toggle(1); err != nil || !ok {
		t.Fatal(err)
	}
	q.run()
	if ok, err := owner.Toggle(0); err != nil || !ok {
		t.Fatal(err)
	}
	q.run()
	if !clock.timers[0].stopped {
		t.Fatal("disable did not stop timer")
	}
	cfg.timeout = 0
	if ok, err := owner.Toggle(1); err != nil || ok {
		t.Fatalf("nonpositive admission = %v, %v", ok, err)
	}
	if len(q.work) != 0 {
		t.Fatal("nonpositive admission queued work")
	}
}

func TestInSegmentTimeoutOwnerCloseInvalidatesQueuedAndScheduled(t *testing.T) {
	q, clock, cfg := &inSegmentQueue{}, &inSegmentClock{}, &inSegmentConfig{timeout: time.Second}
	fired := 0
	owner, err := NewInSegmentTimeoutOwner(clock, q, cfg, func() { fired++ })
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := owner.Toggle(1); err != nil || !ok {
		t.Fatal(err)
	}
	owner.Close()
	q.run()
	if len(clock.timers) != 0 {
		t.Fatal("closed owner scheduled queued work")
	}
	if ok, err := owner.Toggle(1); err != nil || ok {
		t.Fatalf("closed admission = %v, %v", ok, err)
	}
	if fired != 0 {
		t.Fatal("closed owner fired")
	}
}

func TestInSegmentTimeoutOwnerFireCleansAllPendingWork(t *testing.T) {
	q, clock, cfg := &inSegmentQueue{}, &inSegmentClock{}, &inSegmentConfig{timeout: time.Second}
	fired := 0
	owner, err := NewInSegmentTimeoutOwner(clock, q, cfg, func() { fired++ })
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if ok, err := owner.Toggle(1); err != nil || !ok {
			t.Fatal(err)
		}
	}
	q.run()
	if len(clock.timers) != 2 {
		t.Fatalf("timers=%d, want 2", len(clock.timers))
	}
	if ok, err := owner.Toggle(1); err != nil || !ok {
		t.Fatal(err)
	}
	clock.timers[0].fn()
	if fired != 1 {
		t.Fatalf("fired=%d, want 1", fired)
	}
	if !clock.timers[1].stopped {
		t.Fatal("fire did not stop remaining timer")
	}
	owner.Close()
	q.run()
	if len(clock.timers) != 2 {
		t.Fatalf("queued work scheduled after fire: %d timers", len(clock.timers))
	}
	clock.timers[1].fn()
	if fired != 1 {
		t.Fatalf("stale callback fired=%d, want 1", fired)
	}
}

func TestInSegmentTimeoutOwnerEnableByteAndRereadValues(t *testing.T) {
	for _, tc := range []struct {
		name  string
		byte  byte
		delay time.Duration
	}{
		{"disable-byte-two", 2, 0},
		{"enable-zero", 1, 0},
		{"enable-negative", 1, -500 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, clock, cfg := &inSegmentQueue{}, &inSegmentClock{}, &inSegmentConfig{timeout: time.Second}
			owner, err := NewInSegmentTimeoutOwner(clock, q, cfg, func() {})
			if err != nil {
				t.Fatal(err)
			}
			if ok, err := owner.Toggle(1); err != nil || !ok {
				t.Fatal(err)
			}
			q.run()
			if tc.byte == 1 {
				cfg.timeout = time.Second
			}
			if ok, err := owner.Toggle(tc.byte); err != nil || !ok {
				t.Fatal(err)
			}
			if tc.byte == 1 {
				cfg.timeout = tc.delay
			}
			q.run()
			if tc.byte == 1 {
				if len(clock.delays) != 2 || clock.delays[1] != tc.delay {
					t.Fatalf("delays=%v, want reread %v", clock.delays, tc.delay)
				}
			} else if !clock.timers[0].stopped {
				t.Fatal("non-enable byte did not cancel exact timer")
			}
		})
	}
}
