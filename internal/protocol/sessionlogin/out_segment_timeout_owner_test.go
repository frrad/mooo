package sessionlogin

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type outOwnerTestTimer struct {
	fn      func()
	stopped bool
}

func (t *outOwnerTestTimer) Stop() bool {
	wasActive := !t.stopped
	t.stopped = true
	return wasActive
}

func (t *outOwnerTestTimer) runEvenIfStopped() { t.fn() }

type outOwnerTestClock struct {
	timers []*outOwnerTestTimer
	delays []time.Duration
}

func (c *outOwnerTestClock) AfterFunc(delay time.Duration, fn func()) OutSegmentTimeoutTimer {
	t := &outOwnerTestTimer{fn: fn}
	c.timers = append(c.timers, t)
	c.delays = append(c.delays, delay)
	return t
}

type outOwnerTestQueue struct{ work []func() }

func (q *outOwnerTestQueue) Enqueue(fn func()) { q.work = append(q.work, fn) }
func (q *outOwnerTestQueue) runNext() {
	fn := q.work[0]
	q.work = q.work[1:]
	fn()
}

type outOwnerTestConfig struct {
	admission, execution time.Duration
	reads                int
}

func (c *outOwnerTestConfig) OutSegmentTimeout() time.Duration {
	c.reads++
	if c.reads == 1 {
		return c.admission
	}
	return c.execution
}

func newTestOutOwner(t *testing.T, config *outOwnerTestConfig, clock *outOwnerTestClock, queue *outOwnerTestQueue, fired *int) *OutSegmentTimeoutOwner {
	t.Helper()
	owner, err := NewOutSegmentTimeoutOwner(clock, queue, config, "agent", OutSegmentTimeoutSelector, func() {
		(*fired)++
	})
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestNewOutSegmentTimeoutOwnerRejectsUnsupportedSelector(t *testing.T) {
	_, err := NewOutSegmentTimeoutOwner(&outOwnerTestClock{}, &outOwnerTestQueue{}, &outOwnerTestConfig{admission: time.Second}, "agent", "otherSelector:", func() {})
	if err == nil {
		t.Fatal("unsupported selector accepted")
	}
}

func TestOutSegmentTimeoutOwnerEnableRereadsAndSchedulesNilObject(t *testing.T) {
	config := &outOwnerTestConfig{admission: 10 * time.Second, execution: 7 * time.Second}
	clock := &outOwnerTestClock{}
	queue := &outOwnerTestQueue{}
	var fired int
	owner := newTestOutOwner(t, config, clock, queue, &fired)
	if admitted, err := owner.Toggle(1); err != nil || !admitted {
		t.Fatalf("enable admitted=%t err=%v", admitted, err)
	}
	if config.reads != 1 || len(queue.work) != 1 {
		t.Fatalf("admission reads=%d queued=%d", config.reads, len(queue.work))
	}
	queue.runNext()
	if config.reads != 2 || !reflect.DeepEqual(clock.delays, []time.Duration{7 * time.Second}) {
		t.Fatalf("execution reads=%d delays=%v", config.reads, clock.delays)
	}
	clock.timers[0].runEvenIfStopped()
	if fired != 1 {
		t.Fatalf("fired=%d want 1", fired)
	}
}

func TestOutSegmentTimeoutOwnerNonOneCancelsExactTupleWithoutReread(t *testing.T) {
	config := &outOwnerTestConfig{admission: time.Second, execution: 2 * time.Second}
	clock := &outOwnerTestClock{}
	queue := &outOwnerTestQueue{}
	var fired int
	owner := newTestOutOwner(t, config, clock, queue, &fired)
	if _, err := owner.Toggle(1); err != nil {
		t.Fatal(err)
	}
	queue.runNext()
	if _, err := owner.Toggle(2); err != nil {
		t.Fatal(err)
	}
	queue.runNext()
	clock.timers[0].runEvenIfStopped()
	if fired != 0 || config.reads != 3 {
		t.Fatalf("fired=%d reads=%d, want 0 and 3", fired, config.reads)
	}
}

func TestOutSegmentTimeoutOwnerRepeatedEnableCancelsEveryScheduledTuple(t *testing.T) {
	config := &outOwnerTestConfig{admission: time.Second, execution: time.Second}
	clock := &outOwnerTestClock{}
	queue := &outOwnerTestQueue{}
	var fired int
	owner := newTestOutOwner(t, config, clock, queue, &fired)
	for i := 0; i < 2; i++ {
		if _, err := owner.Toggle(1); err != nil {
			t.Fatal(err)
		}
	}
	for len(queue.work) > 0 {
		queue.runNext()
	}
	if len(clock.timers) != 2 {
		t.Fatalf("scheduled timers=%d want 2", len(clock.timers))
	}
	if _, err := owner.Toggle(0); err != nil {
		t.Fatal(err)
	}
	queue.runNext()
	for _, timer := range clock.timers {
		timer.runEvenIfStopped()
	}
	if fired != 0 {
		t.Fatalf("canceled timers fired=%d", fired)
	}
}

func TestOutSegmentTimeoutOwnerCancellationIsScopedToOwner(t *testing.T) {
	configA := &outOwnerTestConfig{admission: time.Second, execution: time.Second}
	configB := &outOwnerTestConfig{admission: time.Second, execution: time.Second}
	clockA := &outOwnerTestClock{}
	clockB := &outOwnerTestClock{}
	queueA := &outOwnerTestQueue{}
	queueB := &outOwnerTestQueue{}
	firedA, firedB := 0, 0
	ownerA := newTestOutOwner(t, configA, clockA, queueA, &firedA)
	ownerB := newTestOutOwner(t, configB, clockB, queueB, &firedB)
	if _, err := ownerA.Toggle(1); err != nil {
		t.Fatal(err)
	}
	if _, err := ownerB.Toggle(1); err != nil {
		t.Fatal(err)
	}
	queueA.runNext()
	queueB.runNext()
	if _, err := ownerA.Toggle(0); err != nil {
		t.Fatal(err)
	}
	queueA.runNext()
	clockA.timers[0].runEvenIfStopped()
	clockB.timers[0].runEvenIfStopped()
	if firedA != 0 || firedB != 1 {
		t.Fatalf("owner fires=(%d,%d), want (0,1)", firedA, firedB)
	}
}

func TestOutSegmentTimeoutOwnerRepeatedEnableAndCloseInvalidateQueuedWork(t *testing.T) {
	config := &outOwnerTestConfig{admission: time.Second, execution: time.Second}
	clock := &outOwnerTestClock{}
	queue := &outOwnerTestQueue{}
	var fired int
	owner := newTestOutOwner(t, config, clock, queue, &fired)
	for i := 0; i < 2; i++ {
		if _, err := owner.Toggle(1); err != nil {
			t.Fatal(err)
		}
	}
	owner.Close()
	for len(queue.work) > 0 {
		queue.runNext()
	}
	if len(clock.timers) != 0 {
		t.Fatalf("timers=%d after close before dispatch", len(clock.timers))
	}
	if admitted, err := owner.Toggle(1); err != nil || admitted {
		t.Fatalf("post-close admitted=%t err=%v", admitted, err)
	}
}

func TestOutSegmentTimeoutOwnerCanonicalToggleVectors(t *testing.T) {
	contract, err := loadWriteCallbackContract(filepath.Join("testdata", "reconnect", "rc-q5-write-callbacks.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range contract.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			config := &outOwnerTestConfig{admission: time.Duration(tc.TimeoutAdmissionSeconds * float64(time.Second)), execution: time.Duration(tc.TimeoutExecutionSeconds * float64(time.Second))}
			clock := &outOwnerTestClock{}
			queue := &outOwnerTestQueue{}
			var fired int
			owner := newTestOutOwner(t, config, clock, queue, &fired)
			if tc.Callback == "partial" || tc.Callback == "complete" {
				if _, err := owner.Toggle(1); err != nil {
					t.Fatal(err)
				}
				queue.runNext()
				if _, err := owner.Disable(); err != nil {
					t.Fatal(err)
				}
				queue.runNext()
				clock.timers[0].runEvenIfStopped()
				if fired != 0 {
					t.Fatalf("callback disable fired=%d", fired)
				}
				return
			}
			admitted, err := owner.Toggle(byte(tc.EnableByte))
			if err != nil {
				t.Fatal(err)
			}
			if tc.TimeoutAdmissionSeconds <= 0 {
				if admitted || len(queue.work) != 0 {
					t.Fatalf("admitted=%t queued=%d", admitted, len(queue.work))
				}
				return
			}
			if !admitted || len(queue.work) != 1 {
				t.Fatalf("admitted=%t queued=%d", admitted, len(queue.work))
			}
			queue.runNext()
			if tc.EnableByte == 1 {
				if len(clock.timers) != 1 || clock.delays[0] != time.Duration(tc.TimeoutExecutionSeconds*float64(time.Second)) {
					t.Fatalf("timers=%d delays=%v", len(clock.timers), clock.delays)
				}
			} else if len(clock.timers) != 0 {
				t.Fatalf("cancel scheduled timers=%d", len(clock.timers))
			}
		})
	}
}
