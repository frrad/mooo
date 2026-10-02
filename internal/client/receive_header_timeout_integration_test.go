package client

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/sessionlogin"
)

type receiveHeaderTimeoutToggleCall struct {
	enable byte
	tag    int64
}

type receiveHeaderTimeoutControllerSpy struct {
	mu            sync.Mutex
	calls         []receiveHeaderTimeoutToggleCall
	closed        int
	onToggle      func(receiveHeaderTimeoutToggleCall)
	rejectDisable bool
}

type writeReturnGateConn struct {
	net.Conn
	release <-chan struct{}
}

type integrationTimeoutTimer struct {
	fn      func()
	stopped bool
}

func (t *integrationTimeoutTimer) Stop() bool {
	wasActive := !t.stopped
	t.stopped = true
	return wasActive
}

type integrationTimeoutClock struct {
	timers []*integrationTimeoutTimer
}

func (c *integrationTimeoutClock) AfterFunc(_ time.Duration, fn func()) sessionlogin.ReceiveHeaderTimeoutTimer {
	t := &integrationTimeoutTimer{fn: fn}
	c.timers = append(c.timers, t)
	return t
}

type integrationTimeoutQueue struct {
	work []func()
}

func (q *integrationTimeoutQueue) Enqueue(fn func()) { q.work = append(q.work, fn) }

type integrationTimeoutConfig struct{}

func (integrationTimeoutConfig) ReceiveHeaderTimeout() time.Duration { return time.Second }

func (c *writeReturnGateConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if err == nil {
		select {
		case <-c.release:
		case <-time.After(time.Second):
			return n, context.DeadlineExceeded
		}
	}
	return n, err
}

func (s *receiveHeaderTimeoutControllerSpy) Toggle(enable byte, tag int64) (bool, error) {
	call := receiveHeaderTimeoutToggleCall{enable: enable, tag: tag}
	s.mu.Lock()
	s.calls = append(s.calls, call)
	onToggle := s.onToggle
	rejectDisable := s.rejectDisable && enable == 0
	s.mu.Unlock()
	if onToggle != nil {
		onToggle(call)
	}
	return !rejectDisable, nil
}

func (s *receiveHeaderTimeoutControllerSpy) Close() {
	s.mu.Lock()
	s.closed++
	s.mu.Unlock()
}

func (s *receiveHeaderTimeoutControllerSpy) snapshot() ([]receiveHeaderTimeoutToggleCall, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	calls := append([]receiveHeaderTimeoutToggleCall(nil), s.calls...)
	return calls, s.closed
}

func TestSessionReceiveHeaderTimeoutArmsAfterSuccessfulSendAndDisarmsBeforeBody(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	_ = clientConn.SetDeadline(time.Now().Add(2 * time.Second))
	_ = serverConn.SetDeadline(time.Now().Add(2 * time.Second))
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	controller := &receiveHeaderTimeoutControllerSpy{}
	armed := make(chan struct{})
	disarmed := make(chan struct{})
	controller.onToggle = func(call receiveHeaderTimeoutToggleCall) {
		if call.enable == 1 {
			close(armed)
		} else {
			close(disarmed)
		}
	}
	session := &Session{
		wire:                       &wireConn{c: clientConn},
		nextID:                     1,
		pushes:                     make(chan loco.Packet, 1),
		pending:                    make(map[uint32]chan requestResult),
		pendingByUniqueID:          make(map[string]chan requestResult),
		pendingUniqueIDByID:        make(map[uint32]string),
		bootstrapDone:              true,
		receiveHeaderTimeout:       controller,
		receiveHeaderTimeoutEnable: func(string, uint32) (byte, bool) { return 1, true },
	}
	readDone := make(chan struct{})
	go func() {
		session.readLoop()
		close(readDone)
	}()
	serverDone := make(chan error, 1)
	go func() {
		header := make([]byte, loco.HeaderSize)
		if _, err := io.ReadFull(serverConn, header); err != nil {
			serverDone <- err
			return
		}
		parsed, err := loco.ParseHeader(header, 0)
		if err != nil {
			serverDone <- err
			return
		}
		body := make([]byte, parsed.BodyLen)
		if _, err := io.ReadFull(serverConn, body); err != nil {
			serverDone <- err
			return
		}
		select {
		case <-armed:
		case <-time.After(time.Second):
			serverDone <- context.DeadlineExceeded
			return
		}
		reply, err := (loco.Packet{Header: loco.Header{PacketID: parsed.PacketID, Method: parsed.Method}, Body: []byte("reply")}).MarshalBinary(0)
		if err == nil {
			if _, err = serverConn.Write(reply[:loco.HeaderSize]); err == nil {
				select {
				case <-disarmed:
				case <-time.After(time.Second):
					err = context.DeadlineExceeded
				}
			}
			if err == nil {
				_, err = serverConn.Write(reply[loco.HeaderSize:])
			}
		}
		serverDone <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := session.requestRaw(ctx, 0, "PING", []byte{}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not finish")
	}
	calls, _ := controller.snapshot()
	if len(calls) != 2 || calls[0] != (receiveHeaderTimeoutToggleCall{enable: 1, tag: int64(requestIDMin)}) || calls[1] != (receiveHeaderTimeoutToggleCall{enable: 0, tag: int64(requestIDMin)}) {
		t.Fatalf("toggle calls=%#v, want arm then header disarm", calls)
	}
	_ = session.Close()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("read loop did not stop")
	}
}

func TestSessionReceiveHeaderTimeoutWrongHeaderUIDDoesNotDisarm(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	_ = clientConn.SetDeadline(time.Now().Add(2 * time.Second))
	_ = serverConn.SetDeadline(time.Now().Add(2 * time.Second))
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	controller := &receiveHeaderTimeoutControllerSpy{}
	waiter := make(chan requestResult, 1)
	session := &Session{
		wire:                &wireConn{c: clientConn},
		pushes:              make(chan loco.Packet, 1),
		pending:             map[uint32]chan requestResult{7: waiter},
		pendingByUniqueID:   map[string]chan requestResult{"EXPECTED.7": waiter},
		pendingUniqueIDByID: map[uint32]string{7: "EXPECTED.7"},
		bootstrapDone:       true, receiveHeaderTimeout: controller,
	}
	readDone := make(chan struct{})
	go func() {
		session.readLoop()
		close(readDone)
	}()
	frame, err := (loco.Packet{Header: loco.Header{PacketID: 7, Method: "OTHER"}, Body: []byte("body")}).MarshalBinary(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serverConn.Write(frame); err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.pushes:
	case <-time.After(time.Second):
		t.Fatal("wrong-UID packet was not routed as unsolicited")
	}
	calls, _ := controller.snapshot()
	if len(calls) != 0 {
		t.Fatalf("wrong-UID toggle calls=%#v", calls)
	}
	_ = session.Close()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("read loop did not stop")
	}
}

func TestSessionReceiveHeaderTimeoutTerminalCloseClosesInjectedOwner(t *testing.T) {
	controller := &receiveHeaderTimeoutControllerSpy{}
	session := &Session{receiveHeaderTimeout: controller}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	_, closed := controller.snapshot()
	if closed != 1 {
		t.Fatalf("owner close count=%d, want 1", closed)
	}
	session.finishRead(io.EOF)
	_, closed = controller.snapshot()
	if closed != 1 {
		t.Fatalf("owner close count after repeated terminal path=%d, want 1", closed)
	}
}

func TestSessionReceiveHeaderTimeoutGateCanRejectAdmission(t *testing.T) {
	controller := &receiveHeaderTimeoutControllerSpy{}
	session := &Session{
		receiveHeaderTimeout: controller,
		receiveHeaderTimeoutEnable: func(string, uint32) (byte, bool) {
			return 1, false
		},
	}
	if session.prepareReceiveHeaderTimeout("PING", 7) != nil {
		t.Fatal("rejected gate admitted")
	}
	calls, _ := controller.snapshot()
	if len(calls) != 0 {
		t.Fatalf("rejected gate produced toggle calls=%#v", calls)
	}
}

func TestSessionReceiveHeaderTimeoutEarlyHeaderDoesNotRearmAfterWriteReturns(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	_ = clientConn.SetDeadline(time.Now().Add(2 * time.Second))
	_ = serverConn.SetDeadline(time.Now().Add(2 * time.Second))
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	releaseWrite := make(chan struct{})
	headerSeen := make(chan struct{})
	controller := &receiveHeaderTimeoutControllerSpy{}
	controller.onToggle = func(call receiveHeaderTimeoutToggleCall) {
		if call.enable == 0 {
			close(releaseWrite)
		}
	}
	session := &Session{
		wire: &wireConn{c: &writeReturnGateConn{Conn: clientConn, release: releaseWrite}}, nextID: 1,
		pushes: make(chan loco.Packet, 1), pending: make(map[uint32]chan requestResult),
		pendingByUniqueID: make(map[string]chan requestResult), pendingUniqueIDByID: make(map[uint32]string), bootstrapDone: true,
		receiveHeaderTimeout:       controller,
		receiveHeaderTimeoutEnable: func(string, uint32) (byte, bool) { return 1, true },
		headerObserver:             func(loco.Header) { close(headerSeen) },
	}
	readDone := make(chan struct{})
	go func() { session.readLoop(); close(readDone) }()
	serverDone := make(chan error, 1)
	go func() {
		header := make([]byte, loco.HeaderSize)
		if _, err := io.ReadFull(serverConn, header); err != nil {
			serverDone <- err
			return
		}
		parsed, err := loco.ParseHeader(header, 0)
		if err != nil {
			serverDone <- err
			return
		}
		body := make([]byte, parsed.BodyLen)
		if _, err := io.ReadFull(serverConn, body); err != nil {
			serverDone <- err
			return
		}
		reply, err := (loco.Packet{Header: loco.Header{PacketID: parsed.PacketID, Method: parsed.Method}, Body: []byte("reply")}).MarshalBinary(0)
		if err == nil {
			_, err = serverConn.Write(reply)
		}
		serverDone <- err
	}()
	requestDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := session.requestRaw(ctx, 0, "PING", []byte{})
		requestDone <- err
	}()
	select {
	case <-headerSeen:
	case <-time.After(time.Second):
		t.Fatal("response header did not arrive before write return")
	}
	calls, _ := controller.snapshot()
	if len(calls) != 1 || calls[0].enable != 0 {
		t.Fatalf("early-header calls before write return=%#v, want one disable", calls)
	}
	select {
	case err := <-requestDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not finish")
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not finish")
	}
	calls, _ = controller.snapshot()
	if len(calls) != 1 {
		t.Fatalf("early-header calls after write return=%#v, want no rearm", calls)
	}
	_ = session.Close()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("read loop did not stop")
	}
}

func TestSessionReceiveHeaderTimeoutRejectedEarlyDisablePreservesArm(t *testing.T) {
	controller := &receiveHeaderTimeoutControllerSpy{rejectDisable: true}
	session := &Session{
		receiveHeaderTimeout:       controller,
		receiveHeaderTimeoutEnable: func(string, uint32) (byte, bool) { return 1, true },
		pendingUniqueIDByID:        map[uint32]string{7: "PING.7"},
	}
	token := session.prepareReceiveHeaderTimeout("PING", 7)
	if token == nil {
		t.Fatal("timeout preparation rejected")
	}
	session.observeHeader(loco.Header{PacketID: 7, Method: "PING"})
	session.commitReceiveHeaderTimeout(token)
	calls, _ := controller.snapshot()
	if len(calls) != 2 || calls[0].enable != 0 || calls[1].enable != 1 {
		t.Fatalf("toggle calls=%#v, want rejected disable followed by preserved arm", calls)
	}
}

func TestSessionReceiveHeaderTimeoutOldTokenCannotAffectReusedPacketID(t *testing.T) {
	controller := &receiveHeaderTimeoutControllerSpy{}
	session := &Session{
		receiveHeaderTimeout:       controller,
		receiveHeaderTimeoutEnable: func(string, uint32) (byte, bool) { return 1, true },
		pendingUniqueIDByID:        map[uint32]string{7: "PING.7"},
	}
	old := session.prepareReceiveHeaderTimeout("PING", 7)
	if old == nil {
		t.Fatal("old timeout preparation rejected")
	}
	// The old response cancels its token before the delayed write commit.
	session.observeHeader(loco.Header{PacketID: 7, Method: "PING"})
	newToken := session.prepareReceiveHeaderTimeout("PING", 7)
	if newToken == nil {
		t.Fatal("new timeout preparation rejected")
	}
	session.commitReceiveHeaderTimeout(old)
	session.abortReceiveHeaderTimeout(old)
	session.commitReceiveHeaderTimeout(newToken)
	session.observeHeader(loco.Header{PacketID: 7, Method: "PING"})
	calls, _ := controller.snapshot()
	if len(calls) != 3 || calls[0].enable != 0 || calls[1].enable != 1 || calls[2].enable != 0 {
		t.Fatalf("token reuse calls=%#v, want old disable, new arm, new disable", calls)
	}
}

func TestSessionReceiveHeaderTimeoutCloseSuppressesQueuedOwnerDelivery(t *testing.T) {
	clock := &integrationTimeoutClock{}
	queue := &integrationTimeoutQueue{}
	owner, err := sessionlogin.NewReceiveHeaderTimeoutOwner(clock, queue, integrationTimeoutConfig{}, "agent", "fireReceiveHeaderTimeout:", func(int64) {})
	if err != nil {
		t.Fatal(err)
	}
	session := &Session{
		receiveHeaderTimeout:       owner,
		receiveHeaderTimeoutEnable: func(string, uint32) (byte, bool) { return 1, true },
	}
	token := session.prepareReceiveHeaderTimeout("PING", 7)
	session.commitReceiveHeaderTimeout(token)
	if len(queue.work) != 1 {
		t.Fatalf("queued owner work=%d, want 1", len(queue.work))
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	queue.work[0]()
	if len(clock.timers) != 0 {
		t.Fatalf("closed owner scheduled timers=%d, want 0", len(clock.timers))
	}
}
