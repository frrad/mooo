package client

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/sessionlogin"
)

type admissionBlockedOutConfig struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *admissionBlockedOutConfig) OutSegmentTimeout() time.Duration {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return time.Second
}

type observedPartialErrorConn struct {
	net.Conn
	err    error
	full   bool
	closed chan struct{}
	once   sync.Once
}

func (c *observedPartialErrorConn) Write(p []byte) (int, error) {
	if c.full {
		return len(p), c.err
	}
	return 1, c.err
}
func (c *observedPartialErrorConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

func TestSessionBufferedWriteErrorPrecedesCarriageClose(t *testing.T) {
	for _, tc := range []struct {
		name string
		full bool
	}{{"partial", false}, {"full_count_with_error", true}} {
		t.Run(tc.name, func(t *testing.T) { testSessionBufferedWriteErrorPrecedesCarriageClose(t, tc.full) })
	}
}

func testSessionBufferedWriteErrorPrecedesCarriageClose(t *testing.T, full bool) {
	clientConn, serverConn := net.Pipe()
	wantErr := errors.New("buffered partial write")
	conn := &observedPartialErrorConn{Conn: clientConn, err: wantErr, full: full, closed: make(chan struct{})}
	config := &admissionBlockedOutConfig{entered: make(chan struct{}), release: make(chan struct{})}
	session := newSession(nil)
	wire := &wireConn{c: conn}
	session.wire = wire
	owner, err := sessionlogin.NewOutSegmentTimeoutOwner(&clientOutClock{}, &clientOutQueue{}, config, "agent", sessionlogin.OutSegmentTimeoutSelector, func() {})
	if err != nil {
		t.Fatal(err)
	}
	submitter, err := session.installOutSegmentSubmitter(wire, owner, func(sessionlogin.OutSegmentWriteResult) {})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(config.release) }) }
	readerDone := make(chan struct{})
	requestDone := make(chan error, 1)
	t.Cleanup(func() {
		release()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
		defer shutdownCancel()
		if err := session.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
		_ = serverConn.Close()
		select {
		case <-readerDone:
		case <-shutdownCtx.Done():
			t.Error("reader did not exit")
		}
	})
	go func() { session.readLoop(); close(readerDone) }()
	go func() { _, err := session.Request(ctx, "PING", nil); requestDone <- err }()
	select {
	case <-config.entered:
	case <-ctx.Done():
		t.Fatal("submission admission not reached")
	}
	// The worker must finish while the submitter still buffers its terminal
	// event. No timing sleeps are needed to force this formerly racy ordering.
	if err := submitter.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-conn.closed:
		t.Fatal("carriage closed before buffered write error delivery")
	default:
	}
	release()
	select {
	case err := <-requestDone:
		if !errors.Is(err, wantErr) {
			t.Fatalf("request error=%v want=%v", err, wantErr)
		}
	case <-ctx.Done():
		t.Fatal("request did not finish")
	}
	select {
	case <-readerDone:
	case <-ctx.Done():
		t.Fatal("partial write did not close reader")
	}
}
