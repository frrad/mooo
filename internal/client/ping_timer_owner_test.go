package client

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type queuedTimer struct {
	fn      func()
	stopped bool
}

type queuedTimerClock struct {
	mu     sync.Mutex
	timers []*queuedTimer
}

func (c *queuedTimerClock) AfterFunc(_ time.Duration, fn func()) timerHandle {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &queuedTimer{fn: fn}
	c.timers = append(c.timers, t)
	return t
}

func (t *queuedTimer) Stop() bool {
	if t.stopped {
		return false
	}
	t.stopped = true
	return true
}

func (c *queuedTimerClock) run(index int) {
	c.mu.Lock()
	t := c.timers[index]
	c.mu.Unlock()
	if !t.stopped {
		t.fn()
	}
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

func TestPingTimerOwnerCancelInvalidatesQueuedGeneration(t *testing.T) {
	clock := &queuedTimerClock{}
	var fired int
	owner := newPingTimerOwner(clock, time.Second, func() { fired++ })
	if !owner.queueSchedule() {
		t.Fatal("queueSchedule rejected open owner")
	}
	owner.queueCancel()
	clock.run(0)
	if fired != 0 {
		t.Fatalf("fired=%d want=0", fired)
	}
}

func TestPingTimerOwnerRejectsStaleTimerAfterReschedule(t *testing.T) {
	clock := &queuedTimerClock{}
	var fired int
	owner := newPingTimerOwner(clock, time.Second, func() { fired++ })
	if !owner.queueSchedule() || !owner.queueSchedule() {
		t.Fatal("queueSchedule rejected open owner")
	}
	clock.run(0)
	if fired != 0 {
		t.Fatalf("stale timer fired=%d want=0", fired)
	}
	clock.run(1)
	if fired != 1 {
		t.Fatalf("current timer fired=%d want=1", fired)
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
	clock.run(0)
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
}
