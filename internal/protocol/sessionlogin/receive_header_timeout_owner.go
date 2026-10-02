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
	// AfterFunc must not invoke fn synchronously on the calling stack. The owner
	// registers the timer while holding its state lock; Stop must likewise not
	// synchronously wait for fn while that lock is held.
	AfterFunc(time.Duration, func()) ReceiveHeaderTimeoutTimer
}

// ReceiveHeaderTimeoutQueue represents the recovered main-queue hop. Queue
// implementations must enqueue only; they must not synchronously reenter the
// owner or execute timer callbacks while the caller is holding another lock.
type ReceiveHeaderTimeoutQueue interface {
	Enqueue(func())
}

// ReceiveHeaderTimeoutConfig supplies the source-observed timeout reads.
// ReceiveHeaderTimeout is read at admission for the gate and again only for a
// queued enable operation.
type ReceiveHeaderTimeoutConfig interface {
	ReceiveHeaderTimeout() time.Duration
}

// ReceiveHeaderTimeoutOwner is a bounded, transport-independent owner for the
// reviewed receive-header timeout effects. It models generation cancellation
// and exact owner/selector/tag identity within one owner instance; operations
// from another instance cannot cancel its scheduled entries. It does not
// close sockets, mutate pending maps, or select a production timer
// implementation.
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
	if selector != receiveHeaderTimeoutSelector {
		return nil, fmt.Errorf("sessionlogin: unsupported receive-header timeout selector %q", selector)
	}
	return &ReceiveHeaderTimeoutOwner{clock: clock, queue: queue, config: config, owner: owner, selector: selector, fire: fire, timers: make(map[*receiveHeaderScheduled]struct{}), queued: make(map[uint64]int64)}, nil
}

// Toggle captures the enable byte at admission, applies the shared positive
// timeout/tag gate to both enable and disable, and enqueues the ordered work.
// Queue implementations must preserve FIFO order: the owner intentionally
// does not reorder pending toggles. A queued enable rereads the timeout before
// scheduling. A queued disable only cancels already-scheduled selectors for
// its exact tag.
func (o *ReceiveHeaderTimeoutOwner) Toggle(enableByte byte, tag int64) (bool, error) {
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
	o.queue.Enqueue(func() { o.applyQueued(id, tag, enableByte == 1) })
	return true, nil
}

func (o *ReceiveHeaderTimeoutOwner) applyQueued(id uint64, tag int64, enable bool) {
	var executionDelay time.Duration
	if enable {
		executionDelay = o.config.ReceiveHeaderTimeout()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	queuedTag, ok := o.queued[id]
	if o.closed || !ok || queuedTag != tag {
		return
	}
	delete(o.queued, id)
	if !enable {
		for entry := range o.timers {
			if entry.tag == tag {
				entry.timer.Stop()
				delete(o.timers, entry)
			}
		}
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
