package client

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestSessionReaderRoutesWrongMethodAndCompletesMatchingUID(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	waiter := make(chan requestResult, 1)
	s := &Session{
		wire:                &wireConn{c: clientConn},
		pushes:              make(chan loco.Packet, 2),
		pending:             map[uint32]chan requestResult{17: waiter},
		pendingByUniqueID:   map[string]chan requestResult{"PING.17": waiter},
		pendingUniqueIDByID: map[uint32]string{17: "PING.17"},
	}
	go s.readLoop()
	wrong, err := (loco.Packet{Header: loco.Header{PacketID: 17, Method: "PUSH"}}).MarshalBinary(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serverConn.Write(wrong); err != nil {
		t.Fatal(err)
	}
	select {
	case pushed := <-s.pushes:
		if pushed.Header.Method != "PUSH" {
			t.Fatalf("push=%+v", pushed.Header)
		}
	case <-time.After(time.Second):
		t.Fatal("wrong-method packet was not routed as push")
	}
	right, err := (loco.Packet{Header: loco.Header{PacketID: 17, Method: "PING"}}).MarshalBinary(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serverConn.Write(right); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-waiter:
		if result.packet.Header.Method != "PING" || result.err != nil {
			t.Fatalf("completion=%+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("matching packet did not complete request")
	}
	select {
	case duplicate := <-waiter:
		t.Fatalf("duplicate completion=%+v", duplicate)
	default:
	}
}

func TestSessionRequestRawRejectsWrongMethodSamePacketID(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	_ = serverConn.SetDeadline(time.Now().Add(time.Second))
	s := newSession(nil)
	s.wire = &wireConn{c: clientConn}
	s.pushes = make(chan loco.Packet, 2)
	readDone := make(chan struct{})
	go func() {
		s.readLoop()
		close(readDone)
	}()
	t.Cleanup(func() {
		_ = s.Close()
		select {
		case <-readDone:
		case <-time.After(time.Second):
			t.Errorf("reader did not stop during cleanup")
		}
	})
	replyBody, err := bson.Marshal(bson.D{{Key: "status", Value: int32(0)}})
	if err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() {
		header := make([]byte, loco.HeaderSize)
		if _, err := io.ReadFull(serverConn, header); err != nil {
			serverDone <- err
			return
		}
		parsed, err := loco.ParseHeader(header, 0)
		if err != nil {
			serverDone <- err
			return
		}
		body := make([]byte, parsed.BodyLen)
		if _, err := io.ReadFull(serverConn, body); err != nil {
			serverDone <- err
			return
		}
		wrong, err := (loco.Packet{Header: loco.Header{PacketID: parsed.PacketID, Method: "PUSH", BodyType: loco.BodyTypeBSON}, Body: replyBody}).MarshalBinary(0)
		if err != nil {
			serverDone <- err
			return
		}
		right, err := (loco.Packet{Header: loco.Header{PacketID: parsed.PacketID, Method: "PING", BodyType: loco.BodyTypeBSON}, Body: replyBody}).MarshalBinary(0)
		if err != nil {
			serverDone <- err
			return
		}
		if _, err = serverConn.Write(wrong); err != nil {
			serverDone <- err
			return
		}
		_, err = serverConn.Write(right)
		serverDone <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := s.Request(ctx, "PING", []byte{5, 0, 0, 0, 0})
	if err != nil || got.Header.Method != "PING" {
		t.Fatalf("request result=%+v err=%v", got.Header, err)
	}
	select {
	case push := <-s.pushes:
		if push.Header.Method != "PUSH" || !bytes.Equal(push.Body, replyBody) {
			t.Fatalf("push=%+v body=%v", push.Header, push.Body)
		}
	case <-time.After(time.Second):
		t.Fatal("wrong-method packet was not pushed")
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestSessionUIDCorrelationDoesNotDeleteReusedPacketID(t *testing.T) {
	oldWaiter := make(chan requestResult, 1)
	newWaiter := make(chan requestResult, 1)
	s := &Session{
		pending:             map[uint32]chan requestResult{17: oldWaiter},
		pendingByUniqueID:   map[string]chan requestResult{"PING.17": oldWaiter},
		pendingUniqueIDByID: map[uint32]string{17: "PING.17"},
	}
	delete(s.pendingByUniqueID, "PING.17")
	s.pending[17] = newWaiter
	s.pendingByUniqueID["LCHATLIST.17"] = newWaiter
	s.pendingUniqueIDByID[17] = "LCHATLIST.17"
	s.removePending(17, oldWaiter)
	if s.pending[17] != newWaiter || s.pendingByUniqueID["LCHATLIST.17"] != newWaiter {
		t.Fatal("late old cancellation removed reused completion")
	}
	if s.dispatchPacket(loco.Packet{Header: loco.Header{PacketID: 17, Method: "PING"}}) {
		t.Fatal("stale method completed reused packet ID")
	}
	if !s.dispatchPacket(loco.Packet{Header: loco.Header{PacketID: 17, Method: "LCHATLIST"}}) {
		t.Fatal("current method did not complete reused packet ID")
	}
	select {
	case result := <-newWaiter:
		if result.packet.Header.Method != "LCHATLIST" {
			t.Fatalf("completion=%+v", result)
		}
	default:
		t.Fatal("reused completion was not delivered")
	}
}

func TestSessionDispatchPacketRequiresMethodInUniqueID(t *testing.T) {
	waiter := make(chan requestResult, 1)
	s := &Session{
		pending:             map[uint32]chan requestResult{17: waiter},
		pendingByUniqueID:   map[string]chan requestResult{"PING.17": waiter},
		pendingUniqueIDByID: map[uint32]string{17: "PING.17"},
	}

	wrong := loco.Packet{Header: loco.Header{PacketID: 17, Method: "PUSH"}}
	if s.dispatchPacket(wrong) {
		t.Fatal("wrong-method packet consumed request completion")
	}
	if _, ok := s.pending[17]; !ok {
		t.Fatal("wrong-method packet removed pending request")
	}
	select {
	case got := <-waiter:
		t.Fatalf("wrong-method packet delivered completion: %+v", got)
	default:
	}

	right := loco.Packet{Header: loco.Header{PacketID: 17, Method: "PING"}}
	if !s.dispatchPacket(right) {
		t.Fatal("matching method packet did not complete request")
	}
	select {
	case got := <-waiter:
		if got.packet.Header.Method != "PING" || got.err != nil {
			t.Fatalf("completion=%+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("matching completion not delivered")
	}
	if _, ok := s.pending[17]; ok {
		t.Fatal("matching packet left pending request")
	}
}
