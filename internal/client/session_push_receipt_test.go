package client

import (
	"context"
	"errors"
	"net"
	"path/filepath"
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

type blockedReceiptCloser struct {
	entered chan struct{}
	release chan struct{}
}

type countingReceiptCloser struct {
	session *Session
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	count   int
}

func (s *countingReceiptCloser) Send(any) error { return nil }

func (s *countingReceiptCloser) Close() {
	s.mu.Lock()
	s.count++
	s.mu.Unlock()
	close(s.entered)
	if s.session != nil {
		_ = s.session.Close()
	}
	<-s.release
}

func (s *countingReceiptCloser) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

func (s *blockedReceiptCloser) Send(any) error { return nil }

func (s *blockedReceiptCloser) Close() {
	close(s.entered)
	<-s.release
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

func waitReceiptSenderClosed(t *testing.T, sender *receiptSenderSpy) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		sender.mu.Lock()
		closed := sender.closed
		sender.mu.Unlock()
		if closed {
			return
		}
		select {
		case <-deadline:
			t.Fatal("receipt binding closer did not finish")
		default:
			time.Sleep(time.Millisecond)
		}
	}
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
	waitReceiptSenderClosed(t, sender)
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

func TestPushReceiptBindingNilWireCloseInvalidatesOwner(t *testing.T) {
	session := newSession(nil)
	sender := &receiptSenderSpy{}
	if err := session.BindPushReceipt(sender, func(loco.Packet) bool { return true }); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	waitReceiptSenderClosed(t, sender)
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

func TestReceiptCloserHonorsShutdownBudget(t *testing.T) {
	session := &Session{}
	closer := &blockedReceiptCloser{entered: make(chan struct{}), release: make(chan struct{})}
	if err := session.BindPushReceipt(closer, func(loco.Packet) bool { return true }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- session.Shutdown(ctx) }()
	select {
	case <-closer.entered:
	case <-time.After(time.Second):
		close(closer.release)
		t.Fatal("receipt closer did not start")
	}
	select {
	case <-done:
		close(closer.release)
	case <-time.After(100 * time.Millisecond):
		close(closer.release)
		<-done
		t.Fatal("injected receipt Close exceeded shutdown context budget")
	}
}

func TestReceiptCloserIsRegisteredBeforeShutdownWaitAndRunsOnce(t *testing.T) {
	session := &Session{}
	closer := &countingReceiptCloser{session: session, entered: make(chan struct{}), release: make(chan struct{})}
	if err := session.BindPushReceipt(closer, func(loco.Packet) bool { return true }); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		go func() { _ = session.Close() }()
	}
	select {
	case <-closer.entered:
	case <-time.After(time.Second):
		t.Fatal("receipt closer did not start")
	}
	if got := closer.calls(); got != 1 {
		t.Fatalf("receipt closer calls before release = %d, want 1", got)
	}
	close(closer.release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown after closer release: %v", err)
	}
	if got := closer.calls(); got != 1 {
		t.Fatalf("receipt closer calls after shutdown = %d, want 1", got)
	}
}

func TestEligibleReceiptCallbackCanCloseRealWireSession(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()
	session := &Session{
		wire:    &wireConn{c: clientConn},
		pushes:  make(chan loco.Packet, 1),
		pending: make(map[uint32]chan requestResult),
	}
	closed := make(chan struct{})
	if err := session.BindPushReceipt(&receiptSenderSpy{}, func(loco.Packet) bool {
		_ = session.Close()
		close(closed)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	session.startReadLoop()
	frame, err := (loco.Packet{Header: loco.Header{Method: "HINT"}, Body: []byte{1}}).MarshalBinary(0)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = serverConn.Write(frame) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("eligible callback did not reenter Session.Close")
	}
}

type fifoReceiptSender struct {
	firstStarted chan struct{}
	firstRelease chan struct{}
	second       chan struct{}
	mu           sync.Mutex
	count        int
}

func (s *fifoReceiptSender) Send(packet any) error {
	s.mu.Lock()
	s.count++
	count := s.count
	s.mu.Unlock()
	if count == 1 {
		close(s.firstStarted)
		<-s.firstRelease
	} else {
		close(s.second)
	}
	return nil
}

func TestReceiptWorkerPreservesFIFOAndSuppressesQueuedAfterClose(t *testing.T) {
	session := newSession(nil)
	sender := &fifoReceiptSender{firstStarted: make(chan struct{}), firstRelease: make(chan struct{}), second: make(chan struct{})}
	if err := session.BindPushReceipt(sender, func(loco.Packet) bool { return true }); err != nil {
		t.Fatal(err)
	}
	session.dispatchPushReceipt(loco.Packet{Header: loco.Header{PacketID: 1}})
	session.dispatchPushReceipt(loco.Packet{Header: loco.Header{PacketID: 2}})
	select {
	case <-sender.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first receipt did not start")
	}
	select {
	case <-sender.second:
		t.Fatal("second receipt ran before first release")
	case <-time.After(20 * time.Millisecond):
	}
	_ = session.Close()
	close(sender.firstRelease)
	select {
	case <-sender.second:
		t.Fatal("queued receipt sent after Session.Close")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestClientShutdownRetainsLeaseUntilReceiptSenderJoins(t *testing.T) {
	dir := t.TempDir()
	leasePath := filepath.Join(dir, "profile.lock")
	lease, err := acquireProfileLease(leasePath)
	if err != nil {
		t.Fatal(err)
	}
	clientConn, serverConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()
	session := &Session{
		wire:    &wireConn{c: clientConn},
		pushes:  make(chan loco.Packet, 1),
		pending: make(map[uint32]chan requestResult),
	}
	sender := &blockedReceiptSender{started: make(chan struct{}), release: make(chan struct{})}
	if err := session.BindPushReceipt(sender, func(loco.Packet) bool { return true }); err != nil {
		t.Fatal(err)
	}
	client := &Client{session: session, lease: lease}
	session.startReadLoop()
	frame, err := (loco.Packet{Header: loco.Header{Method: "HINT"}, Body: []byte{1}}).MarshalBinary(0)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = serverConn.Write(frame) }()
	select {
	case <-sender.started:
	case <-time.After(time.Second):
		t.Fatal("receipt sender did not block")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = client.Shutdown(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want receipt-worker deadline", err)
	}
	if other, leaseErr := acquireProfileLease(leasePath); other != nil || !errors.Is(leaseErr, ErrProfileInUse) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatalf("lease after receipt timeout = %v, want %v", leaseErr, ErrProfileInUse)
	}
	close(sender.release)
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Shutdown(ctx); err != nil {
		t.Fatalf("retry shutdown: %v", err)
	}
	other, err := acquireProfileLease(leasePath)
	if err != nil {
		t.Fatalf("lease after receipt join: %v", err)
	}
	_ = other.Close()
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

func (q *receiptOwnerQueue) count() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.tasks)
}

type receiptOwnerStatus struct{ value int64 }

func (s *receiptOwnerStatus) Status() int64 { return s.value }

type receiptOwnerAccessor struct{ id uint32 }

func (a receiptOwnerAccessor) PacketID(any) (uint32, error) { return a.id, nil }

type receiptOwnerSender struct{ tags []int64 }

func (s *receiptOwnerSender) SendPushReceipt(_ any, tag int64) { s.tags = append(s.tags, tag) }

type blockedReceiptOwnerSender struct {
	started chan struct{}
	release chan struct{}
}

func (s *blockedReceiptOwnerSender) SendPushReceipt(any, int64) {
	close(s.started)
	<-s.release
}

type reentrantReceiptOwnerSender struct {
	session *Session
	done    chan struct{}
}

func (s *reentrantReceiptOwnerSender) SendPushReceipt(any, int64) {
	_ = s.session.Close()
	close(s.done)
}

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
	manager, err := sessionlogin.NewPushReceiptOwnerWithChild("manager-instance", func() string { return "agent-instance" }, queue, scheduler, receiptPingConfig{}, func(_ string, packet any) {
		_ = agent.Send(packet)
	}, agent)
	if err != nil {
		t.Fatal(err)
	}
	session := newSession(nil)
	if err := session.BindPushReceipt(manager, func(packet loco.Packet) bool { return packet.Header.Method == "HINT" && len(packet.Body) > 0 }); err != nil {
		t.Fatal(err)
	}
	session.dispatchPushReceipt(loco.Packet{Header: loco.Header{Method: "HINT"}, Body: []byte{1}})
	deadline := time.After(time.Second)
	for queue.count() == 0 {
		select {
		case <-deadline:
			t.Fatal("composed owner did not enqueue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	status.value = 3
	for queue.runOne() {
	}
	if len(transport.tags) != 1 || transport.tags[0] != -17 || scheduler.cancels != 1 || scheduler.schedules != 1 {
		t.Fatalf("composed owner effects tags=%v cancels=%d schedules=%d", transport.tags, scheduler.cancels, scheduler.schedules)
	}
}

func TestSessionShutdownInvalidatesQueuedComposedReceiptOwnerWork(t *testing.T) {
	queue := &receiptOwnerQueue{}
	status := &receiptOwnerStatus{value: 3}
	transport := &receiptOwnerSender{}
	agent, err := sessionlogin.NewPushReceiptAgentOwner(queue, status, receiptOwnerAccessor{id: 17}, transport)
	if err != nil {
		t.Fatal(err)
	}
	scheduler := &receiptPingScheduler{}
	manager, err := sessionlogin.NewPushReceiptOwnerWithChild("manager-instance", func() string { return "agent-instance" }, queue, scheduler, receiptPingConfig{}, func(_ string, packet any) {
		_ = agent.Send(packet)
	}, agent)
	if err != nil {
		t.Fatal(err)
	}
	session := newSession(nil)
	if err := session.BindPushReceipt(manager, func(packet loco.Packet) bool { return true }); err != nil {
		t.Fatal(err)
	}
	session.dispatchPushReceipt(loco.Packet{})
	deadline := time.After(time.Second)
	for queue.count() < 3 {
		select {
		case <-deadline:
			t.Fatalf("composed owner queued %d work, want manager cancel, agent send, manager schedule", queue.count())
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := session.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	for queue.runOne() {
	}
	if len(transport.tags) != 0 || scheduler.cancels != 0 || scheduler.schedules != 0 {
		t.Fatalf("shutdown admitted obsolete composed work: tags=%v cancels=%d schedules=%d", transport.tags, scheduler.cancels, scheduler.schedules)
	}
}

func TestSessionShutdownJoinsActiveDownstreamReceiptOwner(t *testing.T) {
	queue := &receiptOwnerQueue{}
	status := &receiptOwnerStatus{value: 3}
	sender := &blockedReceiptOwnerSender{started: make(chan struct{}), release: make(chan struct{})}
	agent, err := sessionlogin.NewPushReceiptAgentOwner(queue, status, receiptOwnerAccessor{id: 17}, sender)
	if err != nil {
		t.Fatal(err)
	}
	session := newSession(nil)
	if err := session.BindPushReceipt(agent, func(loco.Packet) bool { return true }); err != nil {
		t.Fatal(err)
	}
	session.dispatchPushReceipt(loco.Packet{})
	deadline := time.After(time.Second)
	for queue.count() == 0 {
		select {
		case <-deadline:
			t.Fatal("downstream receipt owner did not enqueue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	go queue.runOne()
	select {
	case <-sender.started:
	case <-time.After(time.Second):
		t.Fatal("downstream receipt sender did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = session.Shutdown(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown while downstream sender blocked = %v, want deadline", err)
	}
	close(sender.release)
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown after downstream sender release: %v", err)
	}
}

func TestActiveDownstreamReceiptOwnerCanReenterSessionClose(t *testing.T) {
	queue := &receiptOwnerQueue{}
	status := &receiptOwnerStatus{value: 3}
	sender := &reentrantReceiptOwnerSender{done: make(chan struct{})}
	agent, err := sessionlogin.NewPushReceiptAgentOwner(queue, status, receiptOwnerAccessor{id: 17}, sender)
	if err != nil {
		t.Fatal(err)
	}
	session := newSession(nil)
	sender.session = session
	if err := session.BindPushReceipt(agent, func(loco.Packet) bool { return true }); err != nil {
		t.Fatal(err)
	}
	session.dispatchPushReceipt(loco.Packet{})
	deadline := time.After(time.Second)
	for queue.count() == 0 {
		select {
		case <-deadline:
			t.Fatal("reentrant downstream receipt owner did not enqueue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	done := make(chan struct{})
	go func() {
		queue.runOne()
		close(done)
	}()
	select {
	case <-sender.done:
	case <-time.After(time.Second):
		t.Fatal("reentrant receipt owner callback deadlocked Session.Close")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reentrant receipt owner queue did not return")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown after reentrant close: %v", err)
	}
}
