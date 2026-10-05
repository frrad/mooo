package sessionlogin

import (
	"reflect"
	"testing"
	"time"
)

type pushReceiptOwnerQueue struct{ work []func() }

func (q *pushReceiptOwnerQueue) Enqueue(fn func()) { q.work = append(q.work, fn) }
func (q *pushReceiptOwnerQueue) runOne() {
	fn := q.work[0]
	q.work = q.work[1:]
	fn()
}

type pushReceiptOwnerScheduler struct {
	events           []string
	cancel, schedule pushReceiptOwnerCall
}
type pushReceiptOwnerCall struct {
	target, selector string
	object           any
	delay            time.Duration
}

func (s *pushReceiptOwnerScheduler) Cancel(target, selector string, object any) {
	s.events = append(s.events, "cancel")
	s.cancel = pushReceiptOwnerCall{target: target, selector: selector, object: object}
}
func (s *pushReceiptOwnerScheduler) Schedule(target, selector string, object any, delay time.Duration) {
	s.events = append(s.events, "schedule")
	s.schedule = pushReceiptOwnerCall{target: target, selector: selector, object: object, delay: delay}
}

type pushReceiptOwnerConfig struct {
	interval time.Duration
	reads    int
}

func (c *pushReceiptOwnerConfig) PingInterval() time.Duration { c.reads++; return c.interval }

func TestPushReceiptOwnerPreservesManagerOrderingAndExecutionDelay(t *testing.T) {
	q, scheduler, config := &pushReceiptOwnerQueue{}, &pushReceiptOwnerScheduler{}, &pushReceiptOwnerConfig{interval: 12 * time.Second}
	var sent []pushReceiptOwnerCall
	owner, err := NewPushReceiptOwner(q, scheduler, config, func(target string, packet any) {
		sent = append(sent, pushReceiptOwnerCall{target: target, object: packet})
	})
	if err != nil {
		t.Fatal(err)
	}
	packet := struct{ ID uint32 }{ID: 17}
	if err := owner.Send(packet); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sent, []pushReceiptOwnerCall{{target: PushReceiptAgentTarget, object: packet}}) || len(q.work) != 2 {
		t.Fatalf("sent=%v queued=%d", sent, len(q.work))
	}
	q.runOne()
	if !reflect.DeepEqual(scheduler.events, []string{"cancel"}) {
		t.Fatalf("events=%v", scheduler.events)
	}
	if scheduler.cancel != (pushReceiptOwnerCall{target: PushReceiptManagerTarget, selector: PushReceiptPingSelector}) {
		t.Fatalf("cancel=%+v", scheduler.cancel)
	}
	config.interval = -500 * time.Millisecond
	q.runOne()
	if !reflect.DeepEqual(scheduler.events, []string{"cancel", "schedule"}) {
		t.Fatalf("events=%v", scheduler.events)
	}
	if scheduler.schedule != (pushReceiptOwnerCall{target: PushReceiptManagerTarget, selector: PushReceiptPingSelector, delay: -500 * time.Millisecond}) {
		t.Fatalf("schedule=%+v", scheduler.schedule)
	}
	if config.reads != 1 {
		t.Fatalf("config reads=%d, want execution-time one read", config.reads)
	}
}

func TestPushReceiptOwnerRejectsIncompleteDependencies(t *testing.T) {
	if _, err := NewPushReceiptOwner(nil, &pushReceiptOwnerScheduler{}, &pushReceiptOwnerConfig{}, func(string, any) {}); err == nil {
		t.Fatal("nil queue accepted")
	}
}
