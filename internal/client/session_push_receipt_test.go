package client

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/sessionlogin"
)

type receiptSenderSpy struct {
	mu      sync.Mutex
	packets []loco.Packet
	closed  bool
}

type reentrantReceiptSender struct{ session *Session }

func (s *reentrantReceiptSender) Send(any) error {
	_ = s.session.Close()
	return nil
}

type blockedReceiptSender struct {
	started chan struct{}
	release chan struct{}
}

func (s *blockedReceiptSender) Send(any) error {
	close(s.started)
	<-s.release
	return nil
}

func (s *blockedReceiptSender) Close() {}

func (s *receiptSenderSpy) Send(packet any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.packets = append(s.packets, packet.(loco.Packet))
	return nil
}

func (s *receiptSenderSpy) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func TestSessionPushReceiptBindingFiltersUnmatchedPushes(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()
	sender := &receiptSenderSpy{}
	session := &Session{
		wire:    &wireConn{c: clientConn},
		pushes:  make(chan loco.Packet, 4),
		pending: make(map[uint32]chan requestResult),
	}
	if err := session.BindPushReceipt(sender, EligiblePushReceiptPacket); err != nil {
		t.Fatal(err)
	}
	session.startReadLoop()
	defer func() { _ = session.Close() }()

	packets := []loco.Packet{
		{Header: loco.Header{Method: "PUSH", BodyLen: 1}, Body: []byte{1}},
		{Header: loco.Header{Method: "HINT", BodyLen: 1}, Body: []byte{2}},
		{Header: loco.Header{Method: "HINT"}},
		{Header: loco.Header{Method: "BLOCKSYNC", BodyLen: 1}, Body: []byte{3}},
	}
	go func() {
		for _, packet := range packets {
			frame, err := packet.MarshalBinary(0)
			if err != nil {
				return
			}
			_, _ = serverConn.Write(frame)
		}
	}()
	deadline := time.After(time.Second)
	for {
		sender.mu.Lock()
		count := len(sender.packets)
		sender.mu.Unlock()
		if count == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("receipt sends = %d, want 2", count)
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if got := len(session.Pushes()); got != 4 {
		t.Fatalf("ordinary push stream length = %d, want 4", got)
	}
	_ = session.Close()
	sender.mu.Lock()
	closed := sender.closed
	sender.mu.Unlock()
	if !closed {
		t.Fatal("session close did not invalidate receipt binding")
	}
}

func TestSessionRejectsPushReceiptBindingAfterReaderStarts(t *testing.T) {
	session := newSession(nil)
	session.readLoopStarted = true
	if err := session.BindPushReceipt(&receiptSenderSpy{}, EligiblePushReceiptPacket); err == nil {
		t.Fatal("push receipt binding after reader start unexpectedly succeeded")
	}
}

func TestPushReceiptBindingCloseDoesNotDeadlockSenderReentry(t *testing.T) {
	session := newSession(nil)
	sender := &reentrantReceiptSender{session: session}
	if err := session.BindPushReceipt(sender, func(loco.Packet) bool { return true }); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		session.dispatchPushReceipt(loco.Packet{})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("receipt sender reentry deadlocked")
	}
}

func TestPushReceiptBindingCloseDoesNotWaitForBlockedSender(t *testing.T) {
	session := newSession(nil)
	sender := &blockedReceiptSender{started: make(chan struct{}), release: make(chan struct{})}
	if err := session.BindPushReceipt(sender, func(loco.Packet) bool { return true }); err != nil {
		t.Fatal(err)
	}
	dispatchDone := make(chan struct{})
	go func() {
		session.dispatchPushReceipt(loco.Packet{})
		close(dispatchDone)
	}()
	select {
	case <-sender.started:
	case <-time.After(time.Second):
		t.Fatal("receipt sender did not start")
	}
	closeDone := make(chan struct{})
	go func() {
		_ = session.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("session close waited for blocked receipt sender")
	}
	close(sender.release)
	<-dispatchDone
}

type receiptOwnerQueue struct {
	mu    sync.Mutex
	tasks []func()
}

func (q *receiptOwnerQueue) Enqueue(task func()) {
	q.mu.Lock()
	q.tasks = append(q.tasks, task)
	q.mu.Unlock()
}

func (q *receiptOwnerQueue) runOne() bool {
	q.mu.Lock()
	if len(q.tasks) == 0 {
		q.mu.Unlock()
		return false
	}
	task := q.tasks[0]
	q.tasks = q.tasks[1:]
	q.mu.Unlock()
	task()
	return true
}

type receiptOwnerStatus struct{ value int64 }

func (s *receiptOwnerStatus) Status() int64 { return s.value }

type receiptOwnerAccessor struct{ id uint32 }

func (a receiptOwnerAccessor) PacketID(any) (uint32, error) { return a.id, nil }

type receiptOwnerSender struct{ tags []int64 }

func (s *receiptOwnerSender) SendPushReceipt(_ any, tag int64) { s.tags = append(s.tags, tag) }

type receiptPingScheduler struct{ cancels, schedules int }

func (s *receiptPingScheduler) Cancel(string, string, any)                  { s.cancels++ }
func (s *receiptPingScheduler) Schedule(string, string, any, time.Duration) { s.schedules++ }

type receiptPingConfig struct{}

func (receiptPingConfig) PingInterval() time.Duration { return time.Second }

func TestSessionComposesManagerAndAgentReceiptOwners(t *testing.T) {
	queue := &receiptOwnerQueue{}
	status := &receiptOwnerStatus{value: 2}
	transport := &receiptOwnerSender{}
	agent, err := sessionlogin.NewPushReceiptAgentOwner(queue, status, receiptOwnerAccessor{id: 17}, transport)
	if err != nil {
		t.Fatal(err)
	}
	scheduler := &receiptPingScheduler{}
	manager, err := sessionlogin.NewPushReceiptOwner("manager-instance", func() string { return "agent-instance" }, queue, scheduler, receiptPingConfig{}, func(_ string, packet any) {
		_ = agent.Send(packet)
	})
	if err != nil {
		t.Fatal(err)
	}
	session := newSession(nil)
	if err := session.BindPushReceipt(manager, func(packet loco.Packet) bool { return packet.Header.Method == "HINT" && len(packet.Body) > 0 }); err != nil {
		t.Fatal(err)
	}
	session.dispatchPushReceipt(loco.Packet{Header: loco.Header{Method: "HINT"}, Body: []byte{1}})
	if scheduler.cancels != 0 || len(transport.tags) != 0 {
		t.Fatal("owner work executed before injected queue was drained")
	}
	status.value = 3
	for queue.runOne() {
	}
	if len(transport.tags) != 1 || transport.tags[0] != -17 || scheduler.cancels != 1 || scheduler.schedules != 1 {
		t.Fatalf("composed owner effects tags=%v cancels=%d schedules=%d", transport.tags, scheduler.cancels, scheduler.schedules)
	}
}
