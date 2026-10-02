package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
)

type blockedWriteConn struct {
	net.Conn
	entered chan struct{}
}

func (c *blockedWriteConn) Write(p []byte) (int, error) {
	select {
	case <-c.entered:
	default:
		close(c.entered)
	}
	return c.Conn.Write(p)
}

func TestSessionRequestContextInterruptsBlockedWrite(t *testing.T) {
	clientConn, peer := net.Pipe()
	conn := &blockedWriteConn{Conn: clientConn, entered: make(chan struct{})}
	session := newSession(nil)
	session.wire = &wireConn{c: conn}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := session.Request(ctx, "PING", []byte{5, 0, 0, 0, 0})
		result <- err
	}()
	defer func() { cancel(); _ = clientConn.Close(); _ = peer.Close() }()
	select {
	case <-conn.entered:
	case <-time.After(time.Second):
		t.Fatal("write did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("request error=%v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not interrupt blocked write")
	}
}

type partialWriteConn struct {
	net.Conn
	entered chan struct{}
	first   bool
}

func (c *partialWriteConn) Write(p []byte) (int, error) {
	if !c.first {
		c.first = true
		close(c.entered)
		return 1, io.ErrUnexpectedEOF
	}
	return 0, io.ErrUnexpectedEOF
}

func TestSessionPartialWriteClosesCarriage(t *testing.T) {
	clientConn, peer := net.Pipe()
	conn := &partialWriteConn{Conn: clientConn, entered: make(chan struct{})}
	session := newSession(nil)
	session.wire = &wireConn{c: conn}
	ctx := context.Background()
	result := make(chan error, 1)
	go func() {
		_, err := session.Request(ctx, "PING", []byte{5, 0, 0, 0, 0})
		result <- err
	}()
	defer func() { _ = clientConn.Close(); _ = peer.Close() }()
	select {
	case <-conn.entered:
	case <-time.After(time.Second):
		t.Fatal("partial write did not start")
	}
	select {
	case err := <-result:
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("request error=%v, want unexpected EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("partial write did not terminate")
	}
	if _, err := session.Request(context.Background(), "PING", []byte{5, 0, 0, 0, 0}); !errors.Is(err, ErrClosed) {
		t.Fatalf("reused carriage error=%v, want ErrClosed", err)
	}
}

func TestSessionQueuedWriterHonorsContextCancellation(t *testing.T) {
	clientConn, peer := net.Pipe()
	conn := &blockedWriteConn{Conn: clientConn, entered: make(chan struct{})}
	session := newSession(nil)
	session.wire = &wireConn{c: conn}
	firstDone := make(chan error, 1)
	go func() {
		_, err := session.Request(context.Background(), "PING", []byte{5, 0, 0, 0, 0})
		firstDone <- err
	}()
	defer func() { _ = clientConn.Close(); _ = peer.Close() }()
	select {
	case <-conn.entered:
	case <-time.After(time.Second):
		t.Fatal("first write did not start")
	}
	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() {
		_, err := session.Request(ctx, "PING", []byte{5, 0, 0, 0, 0})
		queued <- err
	}()
	deadline := time.After(time.Second)
	for {
		session.mu.Lock()
		admitted := len(session.pending) == 2
		session.mu.Unlock()
		if admitted {
			break
		}
		select {
		case <-deadline:
			t.Fatal("queued request was not admitted before cancellation")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-queued:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued request error=%v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued request did not cancel")
	}
	_ = clientConn.Close()
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first blocked request did not finish after close")
	}
}

func TestSessionCanceledWriteRestoresDeadlineForNextRequest(t *testing.T) {
	clientConn, peer := net.Pipe()
	conn := &blockedWriteConn{Conn: clientConn, entered: make(chan struct{})}
	session := newSession(nil)
	session.wire = &wireConn{c: conn}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() {
		_, err := session.Request(firstCtx, "PING", []byte{5, 0, 0, 0, 0})
		first <- err
	}()
	defer func() { _ = clientConn.Close(); _ = peer.Close() }()
	select {
	case <-conn.entered:
	case <-time.After(time.Second):
		t.Fatal("first write did not start")
	}
	cancelFirst()
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first request error=%v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first request did not cancel")
	}
	secondRead := make(chan error, 1)
	go func() {
		_, err := peer.Read(make([]byte, 256))
		secondRead <- err
	}()
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	second := make(chan error, 1)
	go func() {
		_, err := session.Request(secondCtx, "PING", []byte{5, 0, 0, 0, 0})
		second <- err
	}()
	select {
	case <-secondRead:
	case <-time.After(time.Second):
		t.Fatal("next write remained blocked after canceled write")
	}
	cancelSecond()
	select {
	case <-second:
	case <-time.After(time.Second):
		t.Fatal("next request did not finish")
	}
}

type partialErrorConn struct {
	net.Conn
}

func (c *partialErrorConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	return 1, io.ErrUnexpectedEOF
}

func TestSessionPartialWriteErrorClosesCarriage(t *testing.T) {
	clientConn, peer := net.Pipe()
	conn := &partialErrorConn{Conn: clientConn}
	session := newSession(nil)
	session.wire = &wireConn{c: conn}
	defer func() { _ = clientConn.Close(); _ = peer.Close() }()
	_, err := session.Request(context.Background(), "PING", []byte{5, 0, 0, 0, 0})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("request error=%v, want unexpected EOF", err)
	}
	if _, err := session.Request(context.Background(), "PING", []byte{5, 0, 0, 0, 0}); !errors.Is(err, ErrClosed) {
		t.Fatalf("reused carriage error=%v, want ErrClosed", err)
	}
}

// A reader may have selected a push delivery while a concurrent writer
// discovers a partial frame. The writer must leave terminal push fanout to the
// reader so that the selected send cannot race with channel shutdown.
type selectedPushConn struct {
	net.Conn
	reader   *bytes.Reader
	selected chan struct{}
	release  chan struct{}
	reads    int
}

func (c *selectedPushConn) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	c.reads++
	if c.reads == 2 {
		close(c.selected)
		<-c.release
	}
	return n, err
}

func (c *selectedPushConn) Write([]byte) (int, error)      { return 1, io.ErrUnexpectedEOF }
func (*selectedPushConn) Close() error                     { return nil }
func (*selectedPushConn) SetWriteDeadline(time.Time) error { return nil }

func TestSessionPartialWriteLeavesSelectedPushSafe(t *testing.T) {
	raw, err := (loco.Packet{
		Header: loco.Header{PacketID: 1, Method: "MSG", BodyType: loco.BodyTypeBSON},
		Body:   []byte{5, 0, 0, 0, 0},
	}).MarshalBinary(0)
	if err != nil {
		t.Fatal(err)
	}
	conn := &selectedPushConn{
		reader:   bytes.NewReader(raw),
		selected: make(chan struct{}),
		release:  make(chan struct{}),
	}
	session := newSession(nil)
	session.wire = &wireConn{c: conn}
	session.pushes = make(chan loco.Packet, 1)
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		session.readLoop()
	}()
	select {
	case <-conn.selected:
	case <-time.After(time.Second):
		t.Fatal("push was not selected")
	}
	if _, err := session.Request(context.Background(), "PING", []byte{5, 0, 0, 0, 0}); err == nil {
		t.Fatal("partial write succeeded")
	}
	close(conn.release)
	select {
	case panicValue := <-done:
		if panicValue != nil {
			t.Fatalf("selected push panicked: %v", panicValue)
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not finish")
	}
}
