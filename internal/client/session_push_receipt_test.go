package client

import (
	"net"
	"sync"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
)

type receiptSenderSpy struct {
	mu      sync.Mutex
	packets []loco.Packet
	closed  bool
}

func (s *receiptSenderSpy) Send(packet any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.packets = append(s.packets, packet.(loco.Packet))
	return nil
}

func (s *receiptSenderSpy) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func TestSessionPushReceiptBindingFiltersUnmatchedPushes(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()
	sender := &receiptSenderSpy{}
	session := &Session{
		wire:    &wireConn{c: clientConn},
		pushes:  make(chan loco.Packet, 4),
		pending: make(map[uint32]chan requestResult),
	}
	if err := session.BindPushReceipt(sender, EligiblePushReceiptPacket); err != nil {
		t.Fatal(err)
	}
	session.startReadLoop()
	defer func() { _ = session.Close() }()

	packets := []loco.Packet{
		{Header: loco.Header{Method: "PUSH", BodyLen: 1}, Body: []byte{1}},
		{Header: loco.Header{Method: "HINT", BodyLen: 1}, Body: []byte{2}},
		{Header: loco.Header{Method: "HINT"}},
		{Header: loco.Header{Method: "BLOCKSYNC", BodyLen: 1}, Body: []byte{3}},
	}
	go func() {
		for _, packet := range packets {
			frame, err := packet.MarshalBinary(0)
			if err != nil {
				return
			}
			_, _ = serverConn.Write(frame)
		}
	}()
	deadline := time.After(time.Second)
	for {
		sender.mu.Lock()
		count := len(sender.packets)
		sender.mu.Unlock()
		if count == 2 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("receipt sends = %d, want 2", count)
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if got := len(session.Pushes()); got != 4 {
		t.Fatalf("ordinary push stream length = %d, want 4", got)
	}
	session.Close()
	sender.mu.Lock()
	closed := sender.closed
	sender.mu.Unlock()
	if !closed {
		t.Fatal("session close did not invalidate receipt binding")
	}
}

func TestSessionRejectsPushReceiptBindingAfterReaderStarts(t *testing.T) {
	session := newSession(nil)
	session.readLoopStarted = true
	if err := session.BindPushReceipt(&receiptSenderSpy{}, EligiblePushReceiptPacket); err == nil {
		t.Fatal("push receipt binding after reader start unexpectedly succeeded")
	}
}
