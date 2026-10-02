package client

import (
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type queuedTimer struct {
	fn      func()
	stopped atomic.Bool
}

type queuedTimerClock struct {
	mu     sync.Mutex
	timers []*queuedTimer
	delays []time.Duration
}

func (c *queuedTimerClock) AfterFunc(delay time.Duration, fn func()) timerHandle {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &queuedTimer{fn: fn}
	c.timers = append(c.timers, t)
	c.delays = append(c.delays, delay)
	return t
}

func (t *queuedTimer) Stop() bool {
	return t.stopped.CompareAndSwap(false, true)
}

func (c *queuedTimerClock) run(index int) {
	c.mu.Lock()
	t := c.timers[index]
	c.mu.Unlock()
	if !t.stopped.Load() {
		t.fn()
	}
}

func (c *queuedTimerClock) runLast() {
	c.mu.Lock()
	index := len(c.timers) - 1
	c.mu.Unlock()
	if index < 0 {
		return
	}
	c.run(index)
}

func (c *queuedTimerClock) runEvenIfStopped(index int) {
	c.mu.Lock()
	t := c.timers[index]
	c.mu.Unlock()
	t.fn()
}

func TestPingTimerOwnerSchedulesAndFiresThroughRelativeClock(t *testing.T) {
	clock := &queuedTimerClock{}
	var fired int
	owner := newPingTimerOwner(clock, time.Second, func() { fired++ })
	if !owner.queueSchedule() {
		t.Fatal("queueSchedule rejected open owner")
	}
	clock.run(0)
	if fired != 1 {
		t.Fatalf("fired=%d want=1", fired)
	}
}

func TestRealtimePingTimerOwnerUsesInjectedInterval(t *testing.T) {
	clock := &queuedTimerClock{}
	owner := newPingTimerOwner(clock, time.Hour, func() {})
	if !owner.queueSchedule() {
		t.Fatal("queueSchedule rejected realtime owner")
	}
	clock.mu.Lock()
	delay := clock.delays[0]
	clock.mu.Unlock()
	if delay != time.Hour {
		t.Fatalf("delay=%s want=%s", delay, time.Hour)
	}
	owner.shutdown()
}

func TestRealtimePingTimerOwnerAdapterCanBeStopped(t *testing.T) {
	owner := newRealtimePingTimerOwner(time.Hour, func() {})
	if !owner.queueSchedule() {
		t.Fatal("queueSchedule rejected realtime owner")
	}
	owner.shutdown()
}

func TestPingTimerOwnerCancelInvalidatesQueuedGeneration(t *testing.T) {
	clock := &queuedTimerClock{}
	var fired int
	owner := newPingTimerOwner(clock, time.Second, func() { fired++ })
	if !owner.queueSchedule() {
		t.Fatal("queueSchedule rejected open owner")
	}
	owner.queueCancel()
	clock.runEvenIfStopped(0)
	if fired != 0 {
		t.Fatalf("fired=%d want=0", fired)
	}
}

func TestPingTimerOwnerRejectsStaleTimerAfterReschedule(t *testing.T) {
	clock := &queuedTimerClock{}
	var fired int
	owner := newPingTimerOwner(clock, time.Second, func() { fired++ })
	if !owner.queueSchedule() {
		t.Fatal("first queueSchedule rejected open owner")
	}
	if !owner.queueSchedule() {
		t.Fatal("second queueSchedule rejected open owner")
	}
	clock.runEvenIfStopped(0)
	if fired != 0 {
		t.Fatalf("stale timer fired=%d want=0", fired)
	}
	clock.run(1)
	if fired != 1 {
		t.Fatalf("current timer fired=%d want=1", fired)
	}
}

func TestPingTimerOwnerDeliversSelectedCallbackOnceAcrossShutdown(t *testing.T) {
	clock := &queuedTimerClock{}
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	owner := newPingTimerOwner(clock, time.Second, func() {
		close(started)
		<-release
		close(finished)
	})
	if !owner.queueSchedule() {
		t.Fatal("queueSchedule rejected open owner")
	}
	go clock.runEvenIfStopped(0)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timer callback did not start")
	}
	owner.shutdown()
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("selected callback did not complete")
	}
	clock.runEvenIfStopped(0)
}

func TestPingTimerOwnerCurrentCallbackIsOneShot(t *testing.T) {
	clock := &queuedTimerClock{}
	var fired int
	owner := newPingTimerOwner(clock, time.Second, func() { fired++ })
	if !owner.queueSchedule() {
		t.Fatal("queueSchedule rejected open owner")
	}
	clock.runEvenIfStopped(0)
	clock.runEvenIfStopped(0)
	if fired != 1 {
		t.Fatalf("fired=%d want=1", fired)
	}
}

func TestPingTimerOwnerCloseIsIdempotentAndSuppressesFutureWork(t *testing.T) {
	clock := &queuedTimerClock{}
	var fired int
	owner := newPingTimerOwner(clock, time.Second, func() { fired++ })
	if !owner.queueSchedule() {
		t.Fatal("queueSchedule rejected open owner")
	}
	owner.shutdown()
	owner.shutdown()
	if owner.queueSchedule() {
		t.Fatal("queueSchedule accepted closed owner")
	}
	clock.runEvenIfStopped(0)
	if fired != 0 {
		t.Fatalf("fired=%d want=0", fired)
	}
}

func TestSessionCompletionArmsInjectedTimerOwner(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close(); _ = serverConn.Close() }()
	clock := &queuedTimerClock{}
	fired := make(chan struct{}, 1)
	owner := newPingTimerOwner(clock, time.Second, func() { fired <- struct{}{} })
	waiter := make(chan requestResult, 1)
	session := &Session{
		wire:               &wireConn{c: clientConn},
		pushes:             make(chan loco.Packet, 1),
		pending:            map[uint32]chan requestResult{100000000: waiter},
		lifecycleScheduler: owner,
	}
	go session.readLoop()
	body, err := bson.Marshal(bson.D{{Key: "status", Value: int32(0)}})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := (loco.Packet{Header: loco.Header{PacketID: 100000000, Method: "PING", BodyType: loco.BodyTypeBSON}, Body: body}).MarshalBinary(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serverConn.Write(packet); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-waiter:
		if result.err != nil {
			t.Fatal(result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("completion was not delivered")
	}
	clock.run(0)
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("injected timer callback did not fire")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if owner.queueSchedule() {
		t.Fatal("queueSchedule accepted after Session.Close")
	}
}

func TestSessionTerminalFinishShutsInjectedTimerOwner(t *testing.T) {
	clock := &queuedTimerClock{}
	fired := make(chan struct{}, 1)
	owner := newPingTimerOwner(clock, time.Second, func() { fired <- struct{}{} })
	session := &Session{
		pushes:             make(chan loco.Packet, 1),
		pending:            make(map[uint32]chan requestResult),
		lifecycleScheduler: owner,
	}
	if !owner.queueSchedule() {
		t.Fatal("queueSchedule rejected open owner")
	}
	session.finishRead(errors.New("synthetic terminal disconnect"))
	if owner.queueSchedule() {
		t.Fatal("queueSchedule accepted after terminal finish")
	}
	clock.runEvenIfStopped(0)
	select {
	case <-fired:
		t.Fatal("terminal owner delivered a stale timer callback")
	default:
	}
}
