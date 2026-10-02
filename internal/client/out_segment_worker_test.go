package client

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/sessionlogin"
)

type zeroWriteConn struct {
	net.Conn
	err error
}

func (c *zeroWriteConn) Write([]byte) (int, error)      { return 0, c.err }
func (*zeroWriteConn) SetWriteDeadline(time.Time) error { return nil }
func (*zeroWriteConn) Close() error                     { return nil }

type partialProgressConn struct {
	net.Conn
	first  bool
	closed chan struct{}
}

type partialErrorReadConn struct {
	net.Conn
	err error
}

type partialThenErrorReadConn struct {
	net.Conn
	err   error
	first bool
}

type stuckWriteConn struct {
	net.Conn
	release     chan struct{}
	entered     chan struct{}
	releaseOnce sync.Once
}

func (c *stuckWriteConn) Write([]byte) (int, error) {
	select {
	case <-c.entered:
	default:
		close(c.entered)
	}
	<-c.release
	return 0, net.ErrClosed
}
func (*stuckWriteConn) SetWriteDeadline(time.Time) error { return nil }
func (*stuckWriteConn) Close() error                     { return nil }
func (c *stuckWriteConn) Release()                       { c.releaseOnce.Do(func() { close(c.release) }) }

func (c *partialThenErrorReadConn) Write([]byte) (int, error) {
	if !c.first {
		c.first = true
		return 1, nil
	}
	return 0, c.err
}

func (c *partialErrorReadConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, c.err
	}
	return 1, c.err
}

func (c *partialProgressConn) Write([]byte) (int, error) {
	if !c.first {
		c.first = true
		return 1, nil
	}
	<-c.closed
	return 0, net.ErrClosed
}
func (*partialProgressConn) SetWriteDeadline(time.Time) error { return nil }
func (c *partialProgressConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}

type clientOutTimer struct {
	stopped bool
	fn      func()
}

func (t *clientOutTimer) Stop() bool {
	wasActive := !t.stopped
	t.stopped = true
	return wasActive
}

type clientOutClock struct {
	timers []*clientOutTimer
}

func (c *clientOutClock) AfterFunc(_ time.Duration, fn func()) sessionlogin.OutSegmentTimeoutTimer {
	t := &clientOutTimer{fn: fn}
	c.timers = append(c.timers, t)
	return t
}

type clientOutQueue struct{ work []func() }

func (q *clientOutQueue) Enqueue(fn func()) { q.work = append(q.work, fn) }
func (q *clientOutQueue) runNext() {
	fn := q.work[0]
	q.work = q.work[1:]
	fn()
}

type clientOutConfig struct{ timeout time.Duration }

func (c clientOutConfig) OutSegmentTimeout() time.Duration { return c.timeout }

func TestSessionWriteRequestUsesOptInOutSegmentSubmissionBoundary(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	_ = clientConn.SetDeadline(time.Now().Add(2 * time.Second))
	_ = serverConn.SetDeadline(time.Now().Add(2 * time.Second))

	session := newSession(nil)
	wire := &wireConn{c: clientConn}
	session.wire = wire
	clock := &clientOutClock{}
	queue := &clientOutQueue{}
	owner, err := sessionlogin.NewOutSegmentTimeoutOwner(clock, queue, clientOutConfig{timeout: time.Second}, "agent", sessionlogin.OutSegmentTimeoutSelector, func() {})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan sessionlogin.OutSegmentWriteResult, 1)
	submitter, err := session.installOutSegmentSubmitter(wire, owner, func(got sessionlogin.OutSegmentWriteResult) { result <- got })
	if err != nil {
		t.Fatal(err)
	}

	writeDone := make(chan error, 1)
	go func() {
		writeDone <- session.writeRequest(context.Background(), wire, 7, "PING", nil)
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("writeRequest did not return after async submission")
	}
	if len(queue.work) != 1 {
		t.Fatalf("queued owner work=%d want 1", len(queue.work))
	}
	queue.runNext()
	if len(clock.timers) != 1 {
		t.Fatalf("timers=%d want 1", len(clock.timers))
	}

	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 256)
		_, readErr := serverConn.Read(buf)
		readDone <- readErr
	}()
	select {
	case readErr := <-readDone:
		if readErr != nil {
			t.Fatal(readErr)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not receive submitted frame")
	}
	select {
	case got := <-result:
		if !got.Complete || got.Err != nil {
			t.Fatalf("result=%+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("write completion not forwarded")
	}
	if len(queue.work) != 1 {
		t.Fatalf("queued callback disable=%d want 1", len(queue.work))
	}
	queue.runNext()
	if !clock.timers[0].stopped {
		t.Fatal("completion did not disable out-segment timer")
	}
	submitter.Close()
}

func TestSessionWriteRequestContinuesWhenOutSegmentTimeoutIsDisabled(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	_ = clientConn.SetDeadline(time.Now().Add(2 * time.Second))
	_ = serverConn.SetDeadline(time.Now().Add(2 * time.Second))
	session := newSession(nil)
	wire := &wireConn{c: clientConn}
	session.wire = wire
	clock := &clientOutClock{}
	queue := &clientOutQueue{}
	owner, err := sessionlogin.NewOutSegmentTimeoutOwner(clock, queue, clientOutConfig{}, "agent", sessionlogin.OutSegmentTimeoutSelector, func() {})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan sessionlogin.OutSegmentWriteResult, 1)
	submitter, err := session.installOutSegmentSubmitter(wire, owner, func(got sessionlogin.OutSegmentWriteResult) { result <- got })
	if err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan error, 1)
	go func() { writeDone <- session.writeRequest(context.Background(), wire, 8, "PING", nil) }()
	buf := make([]byte, 256)
	if _, err := serverConn.Read(buf); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("writeRequest did not complete")
	}
	select {
	case got := <-result:
		if !got.Complete || got.Err != nil {
			t.Fatalf("result=%+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("completion not forwarded")
	}
	if len(queue.work) != 0 || len(clock.timers) != 0 {
		t.Fatalf("disabled timer queued=%d timers=%d", len(queue.work), len(clock.timers))
	}
	submitter.Close()
}

func TestSessionOutSegmentWorkerSerializesQueuedSubmissions(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	_ = clientConn.SetDeadline(time.Now().Add(2 * time.Second))
	_ = serverConn.SetDeadline(time.Now().Add(2 * time.Second))
	session := newSession(nil)
	wire := &wireConn{c: clientConn}
	session.wire = wire
	clock := &clientOutClock{}
	queue := &clientOutQueue{}
	owner, err := sessionlogin.NewOutSegmentTimeoutOwner(clock, queue, clientOutConfig{timeout: time.Second}, "agent", sessionlogin.OutSegmentTimeoutSelector, func() {})
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan sessionlogin.OutSegmentWriteResult, 2)
	submitter, err := session.installOutSegmentSubmitter(wire, owner, func(got sessionlogin.OutSegmentWriteResult) { results <- got })
	if err != nil {
		t.Fatal(err)
	}
	if err := session.writeRequest(context.Background(), wire, 9, "ONE", nil); err != nil {
		t.Fatal(err)
	}
	if err := session.writeRequest(context.Background(), wire, 10, "TWO", nil); err != nil {
		t.Fatal(err)
	}
	if len(queue.work) != 2 {
		t.Fatalf("queued owner work=%d want 2", len(queue.work))
	}
	queue.runNext()
	queue.runNext()
	readHeader := func() loco.Header {
		raw := make([]byte, loco.HeaderSize)
		if _, err := io.ReadFull(serverConn, raw); err != nil {
			t.Fatal(err)
		}
		header, err := loco.ParseHeader(raw, 0)
		if err != nil {
			t.Fatal(err)
		}
		return header
	}
	if got := readHeader().PacketID; got != 9 {
		t.Fatalf("first packet id=%d want 9", got)
	}
	if got := readHeader().PacketID; got != 10 {
		t.Fatalf("second packet id=%d want 10", got)
	}
	for i := 0; i < 2; i++ {
		select {
		case got := <-results:
			if !got.Complete || got.Err != nil {
				t.Fatalf("result=%+v", got)
			}
		case <-time.After(time.Second):
			t.Fatal("queued write completion missing")
		}
	}
	submitter.Close()
}

func TestSessionOutSegmentCompleteWriteCancellationKeepsCarriage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn := &completeWriteCancelsConn{cancel: cancel}
	session := newSession(nil)
	wire := &wireConn{c: conn}
	session.wire = wire
	clock := &clientOutClock{}
	queue := &clientOutQueue{}
	owner, err := sessionlogin.NewOutSegmentTimeoutOwner(clock, queue, clientOutConfig{timeout: time.Second}, "agent", sessionlogin.OutSegmentTimeoutSelector, func() {})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan sessionlogin.OutSegmentWriteResult, 1)
	submitter, err := session.installOutSegmentSubmitter(wire, owner, func(got sessionlogin.OutSegmentWriteResult) { result <- got })
	if err != nil {
		t.Fatal(err)
	}
	if err := session.writeRequest(ctx, wire, 11, "PING", nil); err != nil {
		t.Fatal(err)
	}
	if len(queue.work) == 0 {
		t.Fatal("missing enable queue work")
	}
	queue.runNext()
	select {
	case got := <-result:
		if got.Err == nil || got.Ambiguous || !got.Complete {
			t.Fatalf("result=%+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled completion missing")
	}
	for len(queue.work) > 0 {
		queue.runNext()
	}
	session.mu.Lock()
	closed := session.closed || session.closing
	session.mu.Unlock()
	if closed {
		t.Fatal("complete write cancellation closed carriage")
	}
	submitter.Close()
}

func TestSessionOutSegmentQueuedCancellationKeepsCarriageReusable(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	_ = clientConn.SetDeadline(time.Now().Add(3 * time.Second))
	_ = serverConn.SetDeadline(time.Now().Add(3 * time.Second))
	session := newSession(nil)
	wire := &wireConn{c: clientConn}
	session.wire = wire
	clock := &clientOutClock{}
	queue := &clientOutQueue{}
	owner, err := sessionlogin.NewOutSegmentTimeoutOwner(clock, queue, clientOutConfig{timeout: time.Second}, "agent", sessionlogin.OutSegmentTimeoutSelector, func() {})
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan sessionlogin.OutSegmentWriteResult, 3)
	submitter, err := session.installOutSegmentSubmitter(wire, owner, func(got sessionlogin.OutSegmentWriteResult) { results <- got })
	if err != nil {
		t.Fatal(err)
	}
	if err := session.writeRequest(context.Background(), wire, 20, "ONE", nil); err != nil {
		t.Fatal(err)
	}
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	if err := session.writeRequest(secondCtx, wire, 21, "TWO", nil); err != nil {
		t.Fatal(err)
	}
	if len(queue.work) != 2 {
		t.Fatalf("queued owner work=%d want 2", len(queue.work))
	}
	queue.runNext()
	queue.runNext()
	cancelSecond()
	readHeader := func() loco.Header {
		raw := make([]byte, loco.HeaderSize)
		if _, err := io.ReadFull(serverConn, raw); err != nil {
			t.Fatal(err)
		}
		header, err := loco.ParseHeader(raw, 0)
		if err != nil {
			t.Fatal(err)
		}
		return header
	}
	if got := readHeader().PacketID; got != 20 {
		t.Fatalf("first packet id=%d want 20", got)
	}
	var first, second sessionlogin.OutSegmentWriteResult
	for i := 0; i < 2; i++ {
		select {
		case got := <-results:
			if i == 0 {
				first = got
			} else {
				second = got
			}
		case <-time.After(time.Second):
			t.Fatal("queued cancellation result missing")
		}
	}
	if !first.Complete || first.Err != nil || second.Err == nil || second.Ambiguous {
		t.Fatalf("results first=%+v second=%+v", first, second)
	}
	for len(queue.work) > 0 {
		queue.runNext()
	}
	session.mu.Lock()
	closed := session.closed || session.closing
	session.mu.Unlock()
	if closed {
		t.Fatal("queued cancellation closed carriage")
	}
	if err := session.writeRequest(context.Background(), wire, 22, "THREE", nil); err != nil {
		t.Fatal(err)
	}
	queue.runNext()
	if got := readHeader().PacketID; got != 22 {
		t.Fatalf("reused packet id=%d want 22", got)
	}
	select {
	case got := <-results:
		if !got.Complete || got.Err != nil {
			t.Fatalf("reused result=%+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("reused completion missing")
	}
	for len(queue.work) > 0 {
		queue.runNext()
	}
	submitter.Close()
}

func TestSessionRequestZeroByteWriteErrorResolvesPendingRequest(t *testing.T) {
	wantErr := errors.New("zero-byte write failure")
	session := newSession(nil)
	wire := &wireConn{c: &zeroWriteConn{err: wantErr}}
	session.wire = wire
	clock := &clientOutClock{}
	queue := &clientOutQueue{}
	owner, err := sessionlogin.NewOutSegmentTimeoutOwner(clock, queue, clientOutConfig{timeout: time.Second}, "agent", sessionlogin.OutSegmentTimeoutSelector, func() {})
	if err != nil {
		t.Fatal(err)
	}
	submitter, err := session.installOutSegmentSubmitter(wire, owner, func(sessionlogin.OutSegmentWriteResult) {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = session.Request(ctx, "PING", nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("request error=%v, want zero-byte write error", err)
	}
	if len(queue.work) != 2 {
		t.Fatalf("queued owner work=%d want 2 (enable and async failure disable)", len(queue.work))
	}
	queue.runNext()
	queue.runNext()
	submitter.Close()
}

func TestSessionOutSegmentPartialProgressDisablesBeforeTerminalWrite(t *testing.T) {
	conn := &partialProgressConn{closed: make(chan struct{})}
	session := newSession(nil)
	wire := &wireConn{c: conn}
	session.wire = wire
	clock := &clientOutClock{}
	queue := &clientOutQueue{}
	owner, err := sessionlogin.NewOutSegmentTimeoutOwner(clock, queue, clientOutConfig{timeout: time.Second}, "agent", sessionlogin.OutSegmentTimeoutSelector, func() {})
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan sessionlogin.OutSegmentWriteResult, 2)
	submitter, err := session.installOutSegmentSubmitter(wire, owner, func(got sessionlogin.OutSegmentWriteResult) { results <- got })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { submitter.Close() })
	if err := session.writeRequest(context.Background(), wire, 30, "PING", make([]byte, 128)); err != nil {
		t.Fatal(err)
	}
	if len(queue.work) != 1 {
		t.Fatalf("queued owner work=%d want enable", len(queue.work))
	}
	queue.runNext()
	select {
	case got := <-results:
		if !got.Progress || got.Written != 1 || got.Complete {
			t.Fatalf("progress result=%+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("partial progress callback missing")
	}
	if len(queue.work) != 1 {
		t.Fatalf("queued owner work=%d want progress disable", len(queue.work))
	}
	queue.runNext()
	if len(clock.timers) != 1 || !clock.timers[0].stopped {
		t.Fatalf("partial progress did not disable timer: timers=%d stopped=%v", len(clock.timers), len(clock.timers) == 1 && clock.timers[0].stopped)
	}
}

func TestSessionAsyncWriteFailureDisarmsCommittedReceiveTimeout(t *testing.T) {
	controller := &receiveHeaderTimeoutControllerSpy{}
	session := &Session{
		receiveHeaderTimeout:       controller,
		receiveHeaderTimeoutEnable: func(string, uint32) (byte, bool) { return 1, true },
	}
	token := session.prepareReceiveHeaderTimeout("PING", 31)
	if token == nil {
		t.Fatal("timeout preparation rejected")
	}
	session.commitReceiveHeaderTimeout(token)
	session.abortReceiveHeaderTimeout(token)
	calls, _ := controller.snapshot()
	if len(calls) != 2 || calls[0].enable != 1 || calls[1].enable != 0 {
		t.Fatalf("toggle calls=%#v, want committed enable followed by async-failure disable", calls)
	}
}

func TestSessionRequestPreservesPartialWriteErrorBeforeReaderShutdown(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	conn := &partialErrorReadConn{Conn: clientConn, err: errors.New("partial write")}
	t.Cleanup(func() {
		_ = conn.Close()
		_ = serverConn.Close()
	})
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_ = serverConn.SetDeadline(time.Now().Add(2 * time.Second))
	session := newSession(nil)
	wire := &wireConn{c: conn}
	session.wire = wire
	clock := &clientOutClock{}
	queue := &clientOutQueue{}
	owner, err := sessionlogin.NewOutSegmentTimeoutOwner(clock, queue, clientOutConfig{timeout: time.Second}, "agent", sessionlogin.OutSegmentTimeoutSelector, func() {})
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.installOutSegmentSubmitter(wire, owner, func(sessionlogin.OutSegmentWriteResult) {})
	if err != nil {
		t.Fatal(err)
	}
	readerDone := make(chan struct{})
	requestDone := make(chan error, 1)
	requestExited := make(chan struct{})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := session.Shutdown(ctx); err != nil {
			t.Errorf("shutdown cleanup: %v", err)
		}
		cancel()
		select {
		case <-requestExited:
		case <-time.After(time.Second):
			t.Error("request goroutine did not exit during cleanup")
		}
		select {
		case <-readerDone:
		case <-time.After(time.Second):
			t.Error("reader goroutine did not exit during cleanup")
		}
		_ = serverConn.Close()
	})
	go func() { session.readLoop(); close(readerDone) }()
	go func() {
		defer close(requestExited)
		_, requestErr := session.Request(context.Background(), "PING", nil)
		requestDone <- requestErr
	}()
	select {
	case requestErr := <-requestDone:
		if requestErr == nil || !strings.Contains(requestErr.Error(), "partial write") {
			t.Fatalf("request error=%v, want original partial write error", requestErr)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not resolve")
	}
	select {
	case <-readerDone:
	case <-time.After(time.Second):
		t.Fatal("reader did not shut down after terminal partial write")
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), time.Second)
	defer cancelShutdown()
	if err := session.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}

func TestSessionRequestPreservesZeroByteErrorAfterPartialProgress(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	wantErr := errors.New("zero-byte after progress")
	conn := &partialThenErrorReadConn{Conn: clientConn, err: wantErr}
	t.Cleanup(func() {
		_ = conn.Close()
		_ = serverConn.Close()
	})
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_ = serverConn.SetDeadline(time.Now().Add(2 * time.Second))
	session := newSession(nil)
	wire := &wireConn{c: conn}
	session.wire = wire
	clock := &clientOutClock{}
	queue := &clientOutQueue{}
	owner, err := sessionlogin.NewOutSegmentTimeoutOwner(clock, queue, clientOutConfig{timeout: time.Second}, "agent", sessionlogin.OutSegmentTimeoutSelector, func() {})
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.installOutSegmentSubmitter(wire, owner, func(sessionlogin.OutSegmentWriteResult) {})
	if err != nil {
		t.Fatal(err)
	}
	readerDone := make(chan struct{})
	requestDone := make(chan error, 1)
	requestExited := make(chan struct{})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := session.Shutdown(ctx); err != nil {
			t.Errorf("shutdown cleanup: %v", err)
		}
		cancel()
		select {
		case <-requestExited:
		case <-time.After(time.Second):
			t.Error("request goroutine did not exit during cleanup")
		}
		select {
		case <-readerDone:
		case <-time.After(time.Second):
			t.Error("reader goroutine did not exit during cleanup")
		}
		_ = serverConn.Close()
	})
	go func() { session.readLoop(); close(readerDone) }()
	go func() {
		defer close(requestExited)
		_, requestErr := session.Request(context.Background(), "PING", nil)
		requestDone <- requestErr
	}()
	select {
	case requestErr := <-requestDone:
		if requestErr == nil || !strings.Contains(requestErr.Error(), wantErr.Error()) {
			t.Fatalf("request error=%v, want original zero-byte error", requestErr)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not resolve")
	}
	select {
	case <-readerDone:
	case <-time.After(time.Second):
		t.Fatal("reader did not shut down after terminal write error")
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), time.Second)
	defer cancelShutdown()
	if err := session.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
}

func TestSessionShutdownJoinsWorkerWithBoundedContext(t *testing.T) {
	conn := &stuckWriteConn{release: make(chan struct{}), entered: make(chan struct{})}
	session := newSession(nil)
	t.Cleanup(func() {
		conn.Release()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := session.Shutdown(ctx); err != nil {
			t.Errorf("shutdown cleanup: %v", err)
		}
		cancel()
	})
	wire := &wireConn{c: conn}
	session.wire = wire
	clock := &clientOutClock{}
	queue := &clientOutQueue{}
	owner, err := sessionlogin.NewOutSegmentTimeoutOwner(clock, queue, clientOutConfig{timeout: time.Second}, "agent", sessionlogin.OutSegmentTimeoutSelector, func() {})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.installOutSegmentSubmitter(wire, owner, func(sessionlogin.OutSegmentWriteResult) {}); err != nil {
		t.Fatal(err)
	}
	if err := session.writeRequest(context.Background(), wire, 40, "PING", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-conn.entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not enter blocked write")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = session.Shutdown(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error=%v, want bounded join deadline", err)
	}
	conn.Release()
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown after worker release: %v", err)
	}
}
