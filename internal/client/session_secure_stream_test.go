package client

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
)

func TestSessionSecureCoalescedEnvelopeDispatchesBothPackets(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	_ = serverConn.SetDeadline(time.Now().Add(time.Second))
	key := bytes.Repeat([]byte{0x91}, loco.V3KeySize)
	clientSecure, err := loco.NewSecureV3WithKey(key, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	serverSecure, err := loco.NewSecureV3WithKey(key, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	waiter1 := make(chan requestResult, 1)
	waiter2 := make(chan requestResult, 1)
	s := &Session{
		wire:                &wireConn{c: clientConn, secure: clientSecure},
		pushes:              make(chan loco.Packet, 2),
		pending:             map[uint32]chan requestResult{7: waiter1, 8: waiter2},
		pendingByUniqueID:   map[string]chan requestResult{"ONE.7": waiter1, "TWO.8": waiter2},
		pendingUniqueIDByID: map[uint32]string{7: "ONE.7", 8: "TWO.8"},
		bootstrapDone:       true,
	}
	readDone := make(chan struct{})
	serverDone := make(chan error, 1)
	serverStarted, readerFinished, serverFinished := false, false, false
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
		if serverStarted && !serverFinished {
			select {
			case <-serverDone:
			case <-time.After(time.Second):
				t.Errorf("server writer did not stop during cleanup")
			}
		}
		if !readerFinished {
			select {
			case <-readDone:
			case <-time.After(time.Second):
				t.Errorf("session reader did not stop during cleanup")
			}
		}
	})
	go func() {
		s.readLoop()
		close(readDone)
	}()
	first, err := (loco.Packet{Header: loco.Header{PacketID: 7, Method: "ONE"}, Body: []byte("a")}).MarshalBinary(64)
	if err != nil {
		t.Fatal(err)
	}
	second, err := (loco.Packet{Header: loco.Header{PacketID: 8, Method: "TWO"}, Body: []byte("b")}).MarshalBinary(64)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := serverSecure.Encrypt(append(first, second...))
	if err != nil {
		t.Fatal(err)
	}
	serverStarted = true
	go func() {
		_, writeErr := serverConn.Write(envelope)
		serverDone <- writeErr
	}()
	select {
	case result := <-waiter1:
		if result.packet.Header.PacketID != 7 || result.err != nil {
			t.Fatalf("first result=%+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("first packet did not complete")
	}
	select {
	case result := <-waiter2:
		if result.packet.Header.PacketID != 8 || result.err != nil {
			t.Fatalf("second result=%+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("second packet did not complete")
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server writer did not finish")
	}
	serverFinished = true
	_ = clientConn.Close()
	select {
	case <-readDone:
		readerFinished = true
	case <-time.After(time.Second):
		t.Fatal("session reader did not stop")
	}
}
