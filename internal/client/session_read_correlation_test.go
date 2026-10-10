package client

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
)

// Correlation is exercised through Session.readLoop, not a standalone map
// lookup. The validated header method and packet ID form the unique-ID key.
func TestSessionReadLoopCorrelatesPendingPacketSplitAtHeader(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	wantID := uint32(100000123)
	waiter := make(chan requestResult, 1)
	session := &Session{
		wire:   &wireConn{c: clientConn},
		pushes: make(chan loco.Packet, 1),
		pending: map[uint32]chan requestResult{
			wantID: waiter,
		},
		pendingByUniqueID:   map[string]chan requestResult{"PUSH.100000123": waiter},
		pendingUniqueIDByID: map[uint32]string{wantID: "PUSH.100000123"},
	}
	readDone := make(chan struct{})
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
		select {
		case <-readDone:
		case <-time.After(time.Second):
			t.Errorf("session read loop did not stop during cleanup")
		}
	})
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
	case result := <-waiter:
		t.Fatalf("waiter completed before body arrived: %#v", result)
	case <-time.After(20 * time.Millisecond):
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
}

func TestSessionReadLoopBodyEOFFailsWaiterOnce(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	wantID := uint32(100000124)
	waiter := make(chan requestResult, 1)
	session := &Session{
		wire:   &wireConn{c: clientConn},
		pushes: make(chan loco.Packet, 1),
		pending: map[uint32]chan requestResult{
			wantID: waiter,
		},
		pendingByUniqueID:   map[string]chan requestResult{"PUSH.100000124": waiter},
		pendingUniqueIDByID: map[uint32]string{wantID: "PUSH.100000124"},
	}
	readDone := make(chan struct{})
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
		select {
		case <-readDone:
		case <-time.After(time.Second):
			t.Errorf("session read loop did not stop during cleanup")
		}
	})
	go func() {
		session.readLoop()
		close(readDone)
	}()
	header, err := (loco.Header{PacketID: wantID, Method: "PUSH", BodyLen: 4}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	_ = serverConn.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := serverConn.Write(header); err != nil {
		t.Fatal(err)
	}
	if _, err := serverConn.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := serverConn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-waiter:
		if !errors.Is(result.err, io.ErrUnexpectedEOF) {
			t.Fatalf("body EOF waiter error=%v, want unexpected EOF", result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("body EOF did not fail waiter")
	}
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("session read loop did not stop after body EOF")
	}
	select {
	case result := <-waiter:
		t.Fatalf("waiter received duplicate completion: %#v", result)
	default:
	}
	session.mu.Lock()
	pending := session.pending
	session.mu.Unlock()
	if pending != nil {
		t.Fatalf("pending map after terminal body EOF=%#v, want nil", pending)
	}
}
