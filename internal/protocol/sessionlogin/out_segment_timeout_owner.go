package sessionlogin

import (
	"fmt"
	"sync"
	"time"
)

// OutSegmentTimeoutSelector is the reviewed delayed-selector identity for the
// out-segment timeout. The nil object is part of the cancellation tuple.
const OutSegmentTimeoutSelector = "fireOutSegmentTimeout"

// OutSegmentTimeoutTimer is the narrow cancellation handle needed by the
// out-segment owner. A clock may implement this with time.Timer or a
// deterministic test timer.
type OutSegmentTimeoutTimer interface {
	Stop() bool
}

// OutSegmentTimeoutClock creates relative timers. AfterFunc must not invoke
// its callback synchronously on the caller's stack.
type OutSegmentTimeoutClock interface {
	AfterFunc(time.Duration, func()) OutSegmentTimeoutTimer
}

// OutSegmentTimeoutQueue represents the reviewed main-queue hop. It only
// enqueues work; it must not synchronously execute timer callbacks.
type OutSegmentTimeoutQueue interface {
	Enqueue(func())
}

// OutSegmentTimeoutConfig supplies the source-observed timeout reads.
type OutSegmentTimeoutConfig interface {
	OutSegmentTimeout() time.Duration
}

// OutSegmentTimeoutOwner implements the bounded reviewed toggle contract. It
// owns only delayed out-segment work; it does not perform socket I/O or decide
// when callers should enable or disable the timeout.
type OutSegmentTimeoutOwner struct {
	mu       sync.Mutex
	clock    OutSegmentTimeoutClock
	queue    OutSegmentTimeoutQueue
	config   OutSegmentTimeoutConfig
	owner    string
	selector string
	fire     func()
	queued   map[uint64]struct{}
	timers   map[*outSegmentScheduled]struct{}
	nextID   uint64
	closed   bool
}

type outSegmentScheduled struct {
	timer OutSegmentTimeoutTimer
}

func NewOutSegmentTimeoutOwner(clock OutSegmentTimeoutClock, queue OutSegmentTimeoutQueue, config OutSegmentTimeoutConfig, owner, selector string, fire func()) (*OutSegmentTimeoutOwner, error) {
	if clock == nil || queue == nil || config == nil || owner == "" || selector == "" || fire == nil {
		return nil, fmt.Errorf("sessionlogin: incomplete out-segment timeout owner")
	}
	if selector != OutSegmentTimeoutSelector {
		return nil, fmt.Errorf("sessionlogin: unsupported out-segment timeout selector %q", selector)
	}
	return &OutSegmentTimeoutOwner{
		clock: clock, queue: queue, config: config, owner: owner, selector: selector, fire: fire,
		queued: make(map[uint64]struct{}), timers: make(map[*outSegmentScheduled]struct{}),
	}, nil
}

// Toggle reads the timeout at admission. A non-positive value or a closed
// owner rejects the operation without queueing. The enable byte is captured
// before the queue hop; only byte 1 rereads the timeout at execution. Every
// other byte cancels the exact owner/selector/nil-object tuple and does not
// reread configuration.
func (o *OutSegmentTimeoutOwner) Toggle(enableByte byte) (bool, error) {
	if o == nil {
		return false, fmt.Errorf("sessionlogin: nil out-segment timeout owner")
	}
	if o.config.OutSegmentTimeout() <= 0 {
		return false, nil
	}
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return false, nil
	}
	o.nextID++
	id := o.nextID
	o.queued[id] = struct{}{}
	o.mu.Unlock()
	o.queue.Enqueue(func() { o.applyQueued(id, enableByte) })
	return true, nil
}

// Disable is the write-callback operation: it uses the same reviewed
// non-enable toggle and therefore cancels without an execution reread.
func (o *OutSegmentTimeoutOwner) Disable() (bool, error) { return o.Toggle(0) }

func (o *OutSegmentTimeoutOwner) applyQueued(id uint64, enableByte byte) {
	var delay time.Duration
	if enableByte == 1 {
		delay = o.config.OutSegmentTimeout()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return
	}
	if _, ok := o.queued[id]; !ok {
		return
	}
	delete(o.queued, id)
	if enableByte != 1 {
		for entry := range o.timers {
			entry.timer.Stop()
			delete(o.timers, entry)
		}
		return
	}
	entry := &outSegmentScheduled{}
	entry.timer = o.clock.AfterFunc(delay, func() { o.fireEntry(entry) })
	o.timers[entry] = struct{}{}
}

func (o *OutSegmentTimeoutOwner) fireEntry(entry *outSegmentScheduled) {
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
	fire()
}

// Close invalidates queued work and scheduled callbacks. It does not invoke
// the timeout callback.
func (o *OutSegmentTimeoutOwner) Close() {
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
