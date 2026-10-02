package client

import (
	"sync"
	"time"
)

// timerHandle and relativeTimer are the narrow relative-clock boundary. A
// timer implementation must enqueue the callback; it must not invoke it from
// AfterFunc while the owner is scheduling.
type timerHandle interface {
	Stop() bool
}

type relativeTimer interface {
	AfterFunc(time.Duration, func()) timerHandle
}

// pingTimerOwner owns one replaceable relative timer. It has no bootstrap
// policy and is only armed by completed Session requests through the existing
// lifecycleScheduler seam.
type pingTimerOwner struct {
	mu       sync.Mutex
	clock    relativeTimer
	interval time.Duration
	callback func()

	timer      timerHandle
	generation uint64
	closed     bool
}

func newPingTimerOwner(clock relativeTimer, interval time.Duration, callback func()) *pingTimerOwner {
	return &pingTimerOwner{clock: clock, interval: interval, callback: callback}
}

func (o *pingTimerOwner) queueSchedule() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.clock == nil || o.callback == nil {
		return false
	}
	o.generation++
	generation := o.generation
	if o.timer != nil {
		o.timer.Stop()
	}
	o.timer = o.clock.AfterFunc(o.interval, func() { o.fire(generation) })
	return true
}

func (o *pingTimerOwner) queueCancel() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.generation++
	if o.timer != nil {
		o.timer.Stop()
		o.timer = nil
	}
}

// shutdown permanently closes this owner. Session invokes it only on its
// terminal lifecycle path; ordinary request cancellation remains reusable.
func (o *pingTimerOwner) shutdown() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return
	}
	o.closed = true
	o.generation++
	if o.timer != nil {
		o.timer.Stop()
		o.timer = nil
	}
}

func (o *pingTimerOwner) fire(generation uint64) {
	o.mu.Lock()
	if o.closed || generation != o.generation {
		o.mu.Unlock()
		return
	}
	o.timer = nil
	callback := o.callback
	o.mu.Unlock()
	callback()
}
