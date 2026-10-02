package sessionlogin

import (
	"fmt"
	"sync"
	"time"
)

// ReceiveHeaderTimeoutTimer is the narrow cancellation handle required by the
// receive-header owner. Implementations may use time.Timer or a deterministic
// test clock.
type ReceiveHeaderTimeoutTimer interface {
	Stop() bool
}

// ReceiveHeaderTimeoutClock creates relative timers. It does not choose an
// initial admission policy or perform transport work.
type ReceiveHeaderTimeoutClock interface {
	AfterFunc(time.Duration, func()) ReceiveHeaderTimeoutTimer
}

// ReceiveHeaderTimeoutQueue represents the recovered main-queue hop. Queue
// implementations must enqueue only; they must not synchronously reenter the
// owner or execute timer callbacks while the caller is holding another lock.
type ReceiveHeaderTimeoutQueue interface {
	Enqueue(func())
}

// ReceiveHeaderTimeoutConfig supplies the two source-observed configuration
// reads. ReceiveHeaderTimeout is read at admission and again inside the queued
// operation; EnableByte is read only when that queued operation runs.
type ReceiveHeaderTimeoutConfig interface {
	ReceiveHeaderTimeout() time.Duration
	EnableByte() byte
}

// ReceiveHeaderTimeoutOwner is a bounded, transport-independent owner for the
// reviewed receive-header timeout effects. It models generation cancellation
// and exact owner/selector/tag identity, but does not close sockets, mutate
// pending maps, or select a production timer implementation.
type ReceiveHeaderTimeoutOwner struct {
	mu       sync.Mutex
	clock    ReceiveHeaderTimeoutClock
	queue    ReceiveHeaderTimeoutQueue
	config   ReceiveHeaderTimeoutConfig
	owner    string
	selector string
	fire     func(int64)
	timers   map[*receiveHeaderScheduled]struct{}
	queued   map[uint64]int64
	nextID   uint64
	closed   bool
}

type receiveHeaderScheduled struct {
	timer ReceiveHeaderTimeoutTimer
	tag   int64
}

func NewReceiveHeaderTimeoutOwner(clock ReceiveHeaderTimeoutClock, queue ReceiveHeaderTimeoutQueue, config ReceiveHeaderTimeoutConfig, owner, selector string, fire func(int64)) (*ReceiveHeaderTimeoutOwner, error) {
	if clock == nil || queue == nil || config == nil || owner == "" || selector == "" || fire == nil {
		return nil, fmt.Errorf("sessionlogin: incomplete receive-header timeout owner")
	}
	return &ReceiveHeaderTimeoutOwner{clock: clock, queue: queue, config: config, owner: owner, selector: selector, fire: fire, timers: make(map[*receiveHeaderScheduled]struct{}), queued: make(map[uint64]int64)}, nil
}

// Arm performs the admission read and queues the second configuration read.
// It returns false without enqueueing when the positive-timeout/tag gate fails.
func (o *ReceiveHeaderTimeoutOwner) Arm(tag int64) (bool, error) {
	if o == nil {
		return false, fmt.Errorf("sessionlogin: nil receive-header timeout owner")
	}
	if o.config.ReceiveHeaderTimeout() <= 0 || tag < 0 {
		return false, nil
	}
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return false, nil
	}
	o.nextID++
	id := o.nextID
	o.queued[id] = tag
	o.mu.Unlock()
	o.queue.Enqueue(func() { o.applyQueued(id, tag) })
	return true, nil
}

func (o *ReceiveHeaderTimeoutOwner) applyQueued(id uint64, tag int64) {
	executionDelay := o.config.ReceiveHeaderTimeout()
	enable := o.config.EnableByte()
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.queued[id] != tag {
		return
	}
	delete(o.queued, id)
	if enable != 1 {
		return
	}
	entry := &receiveHeaderScheduled{tag: tag}
	entry.timer = o.clock.AfterFunc(executionDelay, func() { o.fireEntry(entry) })
	o.timers[entry] = struct{}{}
}

func (o *ReceiveHeaderTimeoutOwner) fireEntry(entry *receiveHeaderScheduled) {
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
	fire := o.fire
	o.mu.Unlock()
	fire(entry.tag)
}

// Cancel applies only to the exact owner/selector/tag tuple. It invalidates
// queued and timer work; an already-selected callback may still complete.
func (o *ReceiveHeaderTimeoutOwner) Cancel(owner, selector string, tag int64) bool {
	if o == nil || owner != o.owner || selector != o.selector || tag < 0 {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for id, queuedTag := range o.queued {
		if queuedTag == tag {
			delete(o.queued, id)
		}
	}
	for entry := range o.timers {
		if entry.tag == tag {
			entry.timer.Stop()
			delete(o.timers, entry)
		}
	}
	return true
}

// Close invalidates queued and future timer delivery. It does not invoke fire.
func (o *ReceiveHeaderTimeoutOwner) Close() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return
	}
	o.closed = true
	clear(o.queued)
	for entry := range o.timers {
		entry.timer.Stop()
		delete(o.timers, entry)
	}
}
