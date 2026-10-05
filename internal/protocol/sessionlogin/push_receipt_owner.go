package sessionlogin

import (
	"fmt"
	"sync"
	"time"
)

const (
	PushReceiptPingSelector = "sendPingRequest:"
)

// PushReceiptQueue is the injected main-queue hop. Enqueue must only record or
// dispatch work; it must not synchronously reenter PushReceiptOwner.Send.
type PushReceiptQueue interface{ Enqueue(func()) }

// PushReceiptPingScheduler preserves the manager target, selector, and nil
// object tuple used by the delayed PING calls.
type PushReceiptPingScheduler interface {
	Cancel(target, selector string, object any)
	Schedule(target, selector string, object any, delay time.Duration)
}

type PushReceiptPingConfig interface{ PingInterval() time.Duration }

// PushReceiptOwner models the manager-side sendCarriagePushReceipt ordering.
// The carriage-agent send remains an injected inline callback; the separate
// agent status gate and packet-tag derivation are outside this owner.
type PushReceiptOwner struct {
	mu         sync.Mutex
	manager    string
	agent      func() string
	queue      PushReceiptQueue
	scheduler  PushReceiptPingScheduler
	config     PushReceiptPingConfig
	send       func(target string, packet any)
	closed     bool
	generation uint64
}

func NewPushReceiptOwner(managerTarget string, agentResolver func() string, queue PushReceiptQueue, scheduler PushReceiptPingScheduler, config PushReceiptPingConfig, send func(target string, packet any)) (*PushReceiptOwner, error) {
	if managerTarget == "" || agentResolver == nil || queue == nil || scheduler == nil || config == nil || send == nil {
		return nil, fmt.Errorf("sessionlogin: incomplete push-receipt owner")
	}
	return &PushReceiptOwner{manager: managerTarget, agent: agentResolver, queue: queue, scheduler: scheduler, config: config, send: send}, nil
}

// Send enqueues manager PING cancellation, invokes the carriage-agent send
// inline, and then enqueues manager PING scheduling. The interval is read only
// when the scheduling block executes, so a queued operation forwards the
// execution-time configuration value unchanged.
func (o *PushReceiptOwner) Send(packet any) error {
	if o == nil {
		return fmt.Errorf("sessionlogin: nil push-receipt owner")
	}
	o.mu.Lock()
	queue, scheduler, config, agentResolver, send := o.queue, o.scheduler, o.config, o.agent, o.send
	generation, closed := o.generation, o.closed
	o.mu.Unlock()
	if queue == nil || scheduler == nil || config == nil || agentResolver == nil || send == nil {
		return fmt.Errorf("sessionlogin: incomplete push-receipt owner")
	}
	if closed {
		return nil
	}
	queue.Enqueue(func() {
		if !o.active(generation) {
			return
		}
		scheduler.Cancel(o.manager, PushReceiptPingSelector, nil)
	})
	if !o.active(generation) {
		return nil
	}
	send(agentResolver(), packet)
	if !o.active(generation) {
		return nil
	}
	queue.Enqueue(func() {
		if !o.active(generation) {
			return
		}
		delay := config.PingInterval()
		if !o.active(generation) {
			return
		}
		scheduler.Schedule(o.manager, PushReceiptPingSelector, nil, delay)
	})
	return nil
}

func (o *PushReceiptOwner) active(generation uint64) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return !o.closed && o.generation == generation
}

// Close invalidates queued manager cancellation/scheduling and future sends.
// Work already admitted to an injected callback may finish, but no queued
// owner operation remains effective after the generation changes.
func (o *PushReceiptOwner) Close() {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.closed = true
	o.generation++
	o.mu.Unlock()
}
