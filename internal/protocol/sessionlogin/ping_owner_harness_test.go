package sessionlogin

import (
	"errors"
	"sync"
)

var errpingIntentExecutorclosed = errors.New("sessionlogin: ping intent executor closed")

// pingTimerToken identifies one queued schedule and rejects stale callbacks.
type pingTimerToken uint64

type pingQueueAction struct {
	kind  PingIntentKind
	token pingTimerToken
}

// pingIntentExecutor is a deterministic owner for the bounded intent seam.
// Only cancellation and scheduling are queued; ordinary requests and
// completion forwarding remain inline. It has no timer or socket implementation.
type pingIntentExecutor struct {
	mu          sync.Mutex
	closed      bool
	closeErr    error
	queue       []pingQueueAction
	nextToken   pingTimerToken
	scheduled   pingTimerToken
	events      []string
	forwarded   []PingIntent
	pending     map[uint64]func(error)
	nextPending uint64
}

func newPingIntentExecutor() *pingIntentExecutor {
	return &pingIntentExecutor{pending: make(map[uint64]func(error))}
}

// Apply enqueues queue actions and records inline actions in order.
func (e *pingIntentExecutor) Apply(intents []PingIntent) error {
	if e == nil {
		return errpingIntentExecutorclosed
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errpingIntentExecutorclosed
	}
	for _, intent := range intents {
		if intent.Kind != PingIntentQueueCancel && intent.Kind != PingIntentQueueSchedule && intent.Kind != PingIntentOrdinaryRequest && intent.Kind != PingIntentForwardCompletion {
			return errors.New("sessionlogin: unknown ping intent")
		}
	}
	for _, intent := range intents {
		switch intent.Kind {
		case PingIntentQueueCancel:
			e.queue = append(e.queue, pingQueueAction{kind: intent.Kind})
			e.events = append(e.events, string(intent.Kind))
		case PingIntentQueueSchedule:
			e.nextToken++
			e.queue = append(e.queue, pingQueueAction{kind: intent.Kind, token: e.nextToken})
			e.events = append(e.events, string(intent.Kind))
		case PingIntentOrdinaryRequest:
			e.events = append(e.events, string(intent.Kind))
		case PingIntentForwardCompletion:
			e.forwarded = append(e.forwarded, intent)
			e.events = append(e.events, string(intent.Kind))
		}
	}
	return nil
}

// runNext applies one queued action in FIFO order.
func (e *pingIntentExecutor) runNext() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.queue) == 0 {
		return false
	}
	action := e.queue[0]
	e.queue = e.queue[1:]
	if action.kind == PingIntentQueueCancel {
		e.scheduled = 0
	} else {
		e.scheduled = action.token
	}
	e.events = append(e.events, string(action.kind)+"_apply")
	return true
}

func (e *pingIntentExecutor) runAll() {
	for e.runNext() {
	}
}

// fireTimer delivers a timer only when its generation is still current.
func (e *pingIntentExecutor) fireTimer(token pingTimerToken) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || token == 0 || token != e.scheduled {
		return false
	}
	e.scheduled = 0
	e.events = append(e.events, "timer_fire")
	return true
}

func (e *pingIntentExecutor) runNextTimer() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	token := e.scheduled
	e.mu.Unlock()
	return e.fireTimer(token)
}

// addPending registers a request callback for terminal close fan-out.
func (e *pingIntentExecutor) addPending(callback func(error)) uint64 {
	if e == nil || callback == nil {
		return 0
	}
	e.mu.Lock()
	if e.closed {
		err := e.closeErr
		e.mu.Unlock()
		callback(err)
		return 0
	}
	e.nextPending++
	id := e.nextPending
	e.pending[id] = callback
	e.mu.Unlock()
	return id
}

// completePending removes and invokes one pending callback; duplicate
// completion for the same ID is ignored.
func (e *pingIntentExecutor) completePending(id uint64, err error) bool {
	if e == nil || id == 0 {
		return false
	}
	e.mu.Lock()
	callback := e.pending[id]
	if callback != nil {
		delete(e.pending, id)
	}
	e.mu.Unlock()
	if callback == nil {
		return false
	}
	callback(err)
	return true
}

// close cancels future timer delivery and fails remaining pending callbacks
// once. Completion forwarding already selected by Apply remains recorded.
func (e *pingIntentExecutor) close(err error) {
	if e == nil {
		return
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	e.closeErr = err
	e.queue = nil
	e.scheduled = 0
	pending := e.pending
	e.pending = make(map[uint64]func(error))
	e.mu.Unlock()
	for _, callback := range pending {
		callback(err)
	}
}

func (e *pingIntentExecutor) eventLog() []string {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.events...)
}

func (e *pingIntentExecutor) forwardedIntents() []PingIntent {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]PingIntent(nil), e.forwarded...)
}
