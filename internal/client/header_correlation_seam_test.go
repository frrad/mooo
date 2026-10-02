package client

import (
	"net"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
)

// This test records the minimum correlation available at the validated-header
// seam. It deliberately does not claim that packet ID is the source's unique
// ID or timeout tag; those mappings require independent source evidence.
func TestHeaderObserverCanCorrelatePendingPacketBeforeBody(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	wire := &wireConn{c: clientConn}
	wantID := uint32(100000123)
	waiter := make(chan requestResult, 1)
	session := &Session{pending: map[uint32]chan requestResult{wantID: waiter}}
	frame, err := (loco.Packet{Header: loco.Header{PacketID: wantID, Method: "PUSH"}, Body: []byte("body")}).MarshalBinary(64)
	if err != nil {
		t.Fatal(err)
	}
	observed := make(chan bool, 1)
	result := make(chan error, 1)
	go func() {
		_, readErr := wire.readWithHeaderObserver(func(header loco.Header) {
			session.mu.Lock()
			_, present := session.pending[header.PacketID]
			session.mu.Unlock()
			observed <- present
		})
		result <- readErr
	}()
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
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("wire read did not finish")
	}
}
