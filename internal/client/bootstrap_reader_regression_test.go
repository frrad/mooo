package client

import (
	"net"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
)

// TestSessionBootstrapReaderEOFClosesSession covers the empty-pending bootstrap
// gap: an EOF must terminate the Session even before bootstrap handoff.
func TestSessionBootstrapReaderEOFClosesSession(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	session := newSession(nil)
	session.wire = &wireConn{c: clientConn}
	session.bootstrapDone = false
	session.bootstrapPushes = make([]loco.Packet, 0)
	session.pushes = make(chan loco.Packet, requestLimit)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		session.readLoop()
	}()
	if err := serverConn.Close(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = clientConn.Close() }()

	select {
	case <-readerDone:
	case <-time.After(time.Second):
		t.Fatal("bootstrap EOF reader did not terminate")
	}
	session.mu.Lock()
	closed := session.closed
	session.mu.Unlock()
	if !closed {
		t.Fatal("bootstrap EOF did not close the session")
	}
	if session.finishBootstrap() {
		t.Fatal("closed bootstrap session accepted handoff")
	}
	select {
	case _, ok := <-session.pushes:
		if ok {
			t.Fatal("push channel yielded a packet after EOF")
		}
	default:
		t.Fatal("push channel was not closed after EOF")
	}
}
