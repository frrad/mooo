package client

import (
	"net"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
)

// The observer is exercised through Session.readLoop, not a standalone map
// lookup. Packet ID is the only correlation available at this seam; the source
// unique-ID and timeout-tag mappings remain intentionally unresolved.
func TestSessionHeaderObserverCorrelatesPendingPacketBeforeBody(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	wantID := uint32(100000123)
	waiter := make(chan requestResult, 1)
	observed := make(chan bool, 1)
	session := &Session{
		wire:   &wireConn{c: clientConn},
		pushes: make(chan loco.Packet, 1),
		pending: map[uint32]chan requestResult{
			wantID: waiter,
		},
	}
	session.headerObserver = func(header loco.Header) {
		session.mu.Lock()
		_, present := session.pending[header.PacketID]
		session.mu.Unlock()
		observed <- present
	}
	readDone := make(chan struct{})
	go func() {
		session.readLoop()
		close(readDone)
	}()
	frame, err := (loco.Packet{Header: loco.Header{PacketID: wantID, Method: "PUSH"}, Body: []byte("body")}).MarshalBinary(64)
	if err != nil {
		t.Fatal(err)
	}
	_ = serverConn.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := serverConn.Write(frame[:loco.HeaderSize]); err != nil {
		t.Fatal(err)
	}
	select {
	case present := <-observed:
		if !present {
			t.Fatal("validated header did not correlate to pending packet ID")
		}
	case <-time.After(time.Second):
		t.Fatal("header observer did not run")
	}
	if _, err := serverConn.Write(frame[loco.HeaderSize:]); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-waiter:
		if result.err != nil || result.packet.Header.PacketID != wantID || string(result.packet.Body) != "body" {
			t.Fatalf("waiter result=%#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("session did not deliver completed packet")
	}
	if err := serverConn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("session read loop did not stop after transport close")
	}
}
