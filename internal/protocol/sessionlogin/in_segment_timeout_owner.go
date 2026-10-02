package sessionlogin

import (
	"fmt"
	"sync"
	"time"
)

// InSegmentTimeoutTimer is the cancellation handle returned by the injected
// relative clock.
// Stop must not synchronously wait for the callback while the owner lock is held.
type InSegmentTimeoutTimer interface{ Stop() bool }

type InSegmentTimeoutClock interface {
	AfterFunc(time.Duration, func()) InSegmentTimeoutTimer
}

// Enqueue records work and must not synchronously reenter the owner.
type InSegmentTimeoutQueue interface{ Enqueue(func()) }

type InSegmentTimeoutConfig interface{ InSegmentTimeout() time.Duration }

// InSegmentTimeoutOwner models the reviewed in-segment watchdog. It is opt-in:
// callers own transport shutdown and choose when to admit a toggle. A positive
// admission read queues the main-queue operation; only enable rereads the
// timeout at execution. Fire disconnects this owner exactly once.
type InSegmentTimeoutOwner struct {
	mu      sync.Mutex
	clock   InSegmentTimeoutClock
	queue   InSegmentTimeoutQueue
	config  InSegmentTimeoutConfig
	disconn func()
	queued  map[uint64]bool
	timers  map[*inSegmentTimer]struct{}
	nextID  uint64
	closed  bool
}

type inSegmentTimer struct{ timer InSegmentTimeoutTimer }

func NewInSegmentTimeoutOwner(clock InSegmentTimeoutClock, queue InSegmentTimeoutQueue, config InSegmentTimeoutConfig, disconnect func()) (*InSegmentTimeoutOwner, error) {
	if clock == nil || queue == nil || config == nil || disconnect == nil {
		return nil, fmt.Errorf("sessionlogin: incomplete in-segment timeout owner")
	}
	return &InSegmentTimeoutOwner{clock: clock, queue: queue, config: config, disconn: disconnect, queued: make(map[uint64]bool), timers: make(map[*inSegmentTimer]struct{})}, nil
}

func (o *InSegmentTimeoutOwner) Toggle(enableByte byte) (bool, error) {
	if o == nil {
		return false, fmt.Errorf("sessionlogin: nil in-segment timeout owner")
	}
	if o.config.InSegmentTimeout() <= 0 {
		return false, nil
	}
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return false, nil
	}
	o.nextID++
	id := o.nextID
	o.queued[id] = true
	o.mu.Unlock()
	o.queue.Enqueue(func() { o.apply(id, enableByte == 1) })
	return true, nil
}

func (o *InSegmentTimeoutOwner) apply(id uint64, enable bool) {
	o.mu.Lock()
	if o.closed || !o.queued[id] {
		o.mu.Unlock()
		return
	}
	delete(o.queued, id)
	if !enable {
		for entry := range o.timers {
			entry.timer.Stop()
			delete(o.timers, entry)
		}
		o.mu.Unlock()
		return
	}
	delay := o.config.InSegmentTimeout()
	entry := &inSegmentTimer{}
	entry.timer = o.clock.AfterFunc(delay, func() { o.fire(entry) })
	o.timers[entry] = struct{}{}
	o.mu.Unlock()
}

func (o *InSegmentTimeoutOwner) fire(entry *inSegmentTimer) {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return
	}
	if _, ok := o.timers[entry]; !ok {
		o.mu.Unlock()
		return
	}
	delete(o.timers, entry)
	o.closed = true
	clear(o.queued)
	for other := range o.timers {
		other.timer.Stop()
		delete(o.timers, other)
	}
	disconnect := o.disconn
	o.mu.Unlock()
	disconnect()
}

// Close invalidates queued and scheduled owner work without invoking
// disconnect. It is safe to call repeatedly and is interrupt-only.
func (o *InSegmentTimeoutOwner) Close() {
	if o == nil {
		return
	}
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return
	}
	o.closed = true
	clear(o.queued)
	for entry := range o.timers {
		entry.timer.Stop()
		delete(o.timers, entry)
	}
	o.mu.Unlock()
}
