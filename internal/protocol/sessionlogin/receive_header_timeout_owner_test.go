package sessionlogin

import (
	"reflect"
	"testing"
	"time"
)

type ownerTestTimer struct {
	fn      func()
	stopped bool
}

func (t *ownerTestTimer) Stop() bool {
	wasActive := !t.stopped
	t.stopped = true
	return wasActive
}

func (t *ownerTestTimer) runEvenIfStopped() { t.fn() }

type ownerTestClock struct {
	timers []*ownerTestTimer
	delays []time.Duration
}

func (c *ownerTestClock) AfterFunc(delay time.Duration, fn func()) ReceiveHeaderTimeoutTimer {
	t := &ownerTestTimer{fn: fn}
	c.timers = append(c.timers, t)
	c.delays = append(c.delays, delay)
	return t
}

type ownerTestQueue struct{ work []func() }

func (q *ownerTestQueue) Enqueue(fn func()) { q.work = append(q.work, fn) }
func (q *ownerTestQueue) runNext() {
	fn := q.work[0]
	q.work = q.work[1:]
	fn()
}

type ownerTestConfig struct {
	admission, execution time.Duration
	enable               byte
	reads                int
}

func (c *ownerTestConfig) ReceiveHeaderTimeout() time.Duration {
	c.reads++
	if c.reads == 1 {
		return c.admission
	}
	return c.execution
}

func (c *ownerTestConfig) EnableByte() byte { return c.enable }

func newTestOwner(t *testing.T, config *ownerTestConfig, clock *ownerTestClock, queue *ownerTestQueue, fired *[]int64) *ReceiveHeaderTimeoutOwner {
	t.Helper()
	owner, err := NewReceiveHeaderTimeoutOwner(clock, queue, config, "agent", receiveHeaderTimeoutSelector, func(tag int64) {
		*fired = append(*fired, tag)
	})
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestReceiveHeaderTimeoutOwnerReadsAdmissionAndExecutionAndAllowsZeroDelay(t *testing.T) {
	config := &ownerTestConfig{admission: 20 * time.Second, execution: 0, enable: 1}
	clock := &ownerTestClock{}
	queue := &ownerTestQueue{}
	var fired []int64
	owner := newTestOwner(t, config, clock, queue, &fired)
	if admitted, err := owner.Arm(7); err != nil || !admitted {
		t.Fatalf("arm=%t err=%v", admitted, err)
	}
	if config.reads != 1 || len(queue.work) != 1 {
		t.Fatalf("admission reads=%d queued=%d", config.reads, len(queue.work))
	}
	queue.runNext()
	if config.reads != 2 || !reflect.DeepEqual(clock.delays, []time.Duration{0}) {
		t.Fatalf("execution reads=%d delays=%v", config.reads, clock.delays)
	}
	clock.timers[0].runEvenIfStopped()
	if !reflect.DeepEqual(fired, []int64{7}) {
		t.Fatalf("fired=%v", fired)
	}
}

func TestReceiveHeaderTimeoutOwnerPreservesNegativeExecutionDelay(t *testing.T) {
	config := &ownerTestConfig{admission: time.Second, execution: -time.Second, enable: 1}
	clock := &ownerTestClock{}
	queue := &ownerTestQueue{}
	var fired []int64
	owner := newTestOwner(t, config, clock, queue, &fired)
	if _, err := owner.Arm(7); err != nil {
		t.Fatal(err)
	}
	queue.runNext()
	if config.reads != 2 || !reflect.DeepEqual(clock.delays, []time.Duration{-time.Second}) {
		t.Fatalf("execution reads=%d delays=%v", config.reads, clock.delays)
	}
}

func TestReceiveHeaderTimeoutOwnerRepeatedEnableKeepsEachTimerAndExactCancelCancelsAllMatching(t *testing.T) {
	config := &ownerTestConfig{admission: time.Second, execution: 2 * time.Second, enable: 1}
	clock := &ownerTestClock{}
	queue := &ownerTestQueue{}
	var fired []int64
	owner := newTestOwner(t, config, clock, queue, &fired)
	if _, err := owner.Arm(7); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Arm(7); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Arm(8); err != nil {
		t.Fatal(err)
	}
	for len(queue.work) > 0 {
		queue.runNext()
	}
	if len(clock.timers) != 3 {
		t.Fatalf("scheduled timers=%d want 3", len(clock.timers))
	}
	if !owner.Cancel("agent", receiveHeaderTimeoutSelector, 7) {
		t.Fatal("exact cancellation rejected")
	}
	for _, timer := range clock.timers {
		timer.runEvenIfStopped()
	}
	if !reflect.DeepEqual(fired, []int64{8}) {
		t.Fatalf("fired=%v want [8]", fired)
	}
}

func TestReceiveHeaderTimeoutOwnerCloseSuppressesQueuedAndTimerDelivery(t *testing.T) {
	config := &ownerTestConfig{admission: time.Second, execution: time.Second, enable: 1}
	clock := &ownerTestClock{}
	queue := &ownerTestQueue{}
	var fired []int64
	owner := newTestOwner(t, config, clock, queue, &fired)
	if _, err := owner.Arm(7); err != nil {
		t.Fatal(err)
	}
	owner.Close()
	queue.runNext()
	if len(clock.timers) != 0 {
		t.Fatalf("closed owner scheduled timers=%d", len(clock.timers))
	}
	if !reflect.DeepEqual(fired, []int64(nil)) {
		t.Fatalf("fired=%v", fired)
	}
}
