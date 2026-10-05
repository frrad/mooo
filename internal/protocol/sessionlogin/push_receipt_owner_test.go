package sessionlogin

import (
	"reflect"
	"testing"
	"time"
)

type pushReceiptOwnerQueue struct {
	work   []func()
	events *[]string
}

func (q *pushReceiptOwnerQueue) Enqueue(fn func()) {
	q.work = append(q.work, fn)
	if q.events != nil {
		*q.events = append(*q.events, "enqueue")
	}
}
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
	events := []string{}
	q, scheduler, config := &pushReceiptOwnerQueue{events: &events}, &pushReceiptOwnerScheduler{}, &pushReceiptOwnerConfig{interval: 12 * time.Second}
	var sent []pushReceiptOwnerCall
	owner, err := NewPushReceiptOwner("manager-1", func() string { return "agent-1" }, q, scheduler, config, func(target string, packet any) {
		events = append(events, "inline")
		sent = append(sent, pushReceiptOwnerCall{target: target, object: packet})
	})
	if err != nil {
		t.Fatal(err)
	}
	packet := struct{ ID uint32 }{ID: 17}
	if err := owner.Send(packet); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"enqueue", "inline", "enqueue"}) || !reflect.DeepEqual(sent, []pushReceiptOwnerCall{{target: "agent-1", object: packet}}) || len(q.work) != 2 {
		t.Fatalf("sent=%v queued=%d", sent, len(q.work))
	}
	q.runOne()
	if !reflect.DeepEqual(scheduler.events, []string{"cancel"}) {
		t.Fatalf("events=%v", scheduler.events)
	}
	if scheduler.cancel != (pushReceiptOwnerCall{target: "manager-1", selector: "sendPingRequest:"}) {
		t.Fatalf("cancel=%+v", scheduler.cancel)
	}
	config.interval = -500 * time.Millisecond
	q.runOne()
	if !reflect.DeepEqual(scheduler.events, []string{"cancel", "schedule"}) {
		t.Fatalf("events=%v", scheduler.events)
	}
	if scheduler.schedule != (pushReceiptOwnerCall{target: "manager-1", selector: "sendPingRequest:", delay: -500 * time.Millisecond}) {
		t.Fatalf("schedule=%+v", scheduler.schedule)
	}
	if config.reads != 1 {
		t.Fatalf("config reads=%d, want execution-time one read", config.reads)
	}
}

func TestPushReceiptOwnerRejectsIncompleteDependencies(t *testing.T) {
	if _, err := NewPushReceiptOwner("manager", func() string { return "agent" }, nil, &pushReceiptOwnerScheduler{}, &pushReceiptOwnerConfig{}, func(string, any) {}); err == nil {
		t.Fatal("nil queue accepted")
	}
}

func TestPushReceiptOwnersKeepManagerTargetsDistinctOnSharedScheduler(t *testing.T) {
	q := &pushReceiptOwnerQueue{}
	scheduler := &pushReceiptOwnerScheduler{}
	config := &pushReceiptOwnerConfig{interval: time.Second}
	first, err := NewPushReceiptOwner("manager-1", func() string { return "agent-1" }, q, scheduler, config, func(string, any) {})
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPushReceiptOwner("manager-2", func() string { return "agent-2" }, q, scheduler, config, func(string, any) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Send(nil); err != nil {
		t.Fatal(err)
	}
	if err := second.Send(nil); err != nil {
		t.Fatal(err)
	}
	q.runOne()
	if scheduler.cancel.target != "manager-1" || scheduler.cancel.selector != "sendPingRequest:" || scheduler.cancel.object != nil {
		t.Fatalf("first cancel=%+v", scheduler.cancel)
	}
	q.runOne()
	q.runOne()
	if scheduler.cancel.target != "manager-2" {
		t.Fatalf("second cancel target=%q", scheduler.cancel.target)
	}
	q.runOne()
	if scheduler.schedule.target != "manager-2" || scheduler.schedule.selector != "sendPingRequest:" || scheduler.schedule.object != nil {
		t.Fatalf("second schedule=%+v", scheduler.schedule)
	}
}

func TestPushReceiptOwnerAllowsReverseQueueExecutionWithoutChangingCapturedTarget(t *testing.T) {
	q, scheduler, config := &pushReceiptOwnerQueue{}, &pushReceiptOwnerScheduler{}, &pushReceiptOwnerConfig{interval: 0}
	owner, err := NewPushReceiptOwner("manager", func() string { return "agent" }, q, scheduler, config, func(string, any) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Send(nil); err != nil {
		t.Fatal(err)
	}
	q.work[1]()
	if scheduler.schedule.target != "manager" || scheduler.schedule.selector != "sendPingRequest:" || scheduler.schedule.object != nil || scheduler.schedule.delay != 0 {
		t.Fatalf("reverse schedule=%+v", scheduler.schedule)
	}
}

func TestPushReceiptOwnerResolvesAgentAtEachSend(t *testing.T) {
	q, scheduler, config := &pushReceiptOwnerQueue{}, &pushReceiptOwnerScheduler{}, &pushReceiptOwnerConfig{interval: time.Second}
	agent := "agent-1"
	var sent []string
	owner, err := NewPushReceiptOwner("manager", func() string { return agent }, q, scheduler, config, func(target string, _ any) {
		sent = append(sent, target)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Send(nil); err != nil {
		t.Fatal(err)
	}
	q.runOne()
	q.runOne()
	agent = "agent-2"
	if err := owner.Send(nil); err != nil {
		t.Fatal(err)
	}
	q.runOne()
	q.runOne()
	if !reflect.DeepEqual(sent, []string{"agent-1", "agent-2"}) {
		t.Fatalf("resolved agents=%v", sent)
	}
	if scheduler.cancel.target != "manager" || scheduler.schedule.target != "manager" {
		t.Fatalf("manager target changed: cancel=%q schedule=%q", scheduler.cancel.target, scheduler.schedule.target)
	}
}
