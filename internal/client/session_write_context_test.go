package client

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
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
		return 1, nil
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
