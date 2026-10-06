package client

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestSessionFinishReadFansOutOnceAndClearsPending(t *testing.T) {
	first := make(chan requestResult, 1)
	second := make(chan requestResult, 1)
	pushes := make(chan loco.Packet, 1)
	session := &Session{
		pending: map[uint32]chan requestResult{100000000: first, 100000001: second},
		pushes:  pushes,
	}
	wantErr := errors.New("synthetic carriage close")

	session.finishRead(wantErr)
	for name, waiter := range map[string]chan requestResult{"first": first, "second": second} {
		select {
		case result := <-waiter:
			if !errors.Is(result.err, wantErr) {
				t.Fatalf("%s waiter error = %v, want %v", name, result.err, wantErr)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s waiter was not failed", name)
		}
	}
	if session.pending != nil {
		t.Fatalf("pending map = %#v, want nil after fan-out", session.pending)
	}
	select {
	case _, ok := <-pushes:
		if ok {
			t.Fatal("push stream yielded a value after close")
		}
	case <-time.After(time.Second):
		t.Fatal("push stream was not closed")
	}

	// A second transport error must not invoke stale callbacks again.
	session.finishRead(errors.New("second synthetic close"))
	select {
	case result := <-first:
		t.Fatalf("stale first callback invoked again: %#v", result)
	default:
	}
}

func TestSessionCloseFailsPendingRequestThroughReaderTeardown(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	session := &Session{
		wire:    &wireConn{c: clientConn},
		pushes:  make(chan loco.Packet, 1),
		pending: make(map[uint32]chan requestResult),
	}
	waiter := make(chan requestResult, 1)
	session.pending[100000000] = waiter
	go session.readLoop()

	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverConn.Close() }()
	select {
	case result := <-waiter:
		if result.err == nil {
			t.Fatal("closed carriage completed pending request successfully")
		}
	case <-time.After(time.Second):
		t.Fatal("pending request was not failed by reader teardown")
	}
}

func TestSessionResponseRemovesPendingCallback(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	session := &Session{
		wire:    &wireConn{c: clientConn},
		nextID:  100000000,
		pushes:  make(chan loco.Packet, 1),
		pending: make(map[uint32]chan requestResult),
	}
	go session.readLoop()
	defer func() {
		_ = session.Close()
		_ = serverConn.Close()
	}()

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
		if _, err := io.CopyN(io.Discard, serverConn, int64(parsed.BodyLen)); err != nil {
			serverDone <- err
			return
		}
		body, err := bson.Marshal(bson.D{{Key: "status", Value: int32(0)}})
		if err == nil {
			var reply []byte
			reply, err = (loco.Packet{Header: loco.Header{
				PacketID: parsed.PacketID, Method: parsed.Method, BodyType: loco.BodyTypeBSON,
			}, Body: body}).MarshalBinary(0)
			if err == nil {
				_, err = serverConn.Write(reply)
			}
		}
		serverDone <- err
	}()

	if _, err := session.Request(context.Background(), "PING", []byte{5, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	session.mu.Lock()
	pending := len(session.pending)
	session.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending callbacks after response = %d, want zero", pending)
	}
}

func TestSessionRequestIDWrapsBeforeOfficialUpperBound(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	session := &Session{
		wire:    &wireConn{c: clientConn},
		nextID:  199999999,
		pushes:  make(chan loco.Packet, 1),
		pending: make(map[uint32]chan requestResult),
	}
	go session.readLoop()
	defer func() {
		_ = session.Close()
		_ = serverConn.Close()
	}()

	ids := make(chan uint32, 2)
	serverDone := make(chan error, 1)
	body, err := bson.Marshal(bson.D{{Key: "status", Value: int32(0)}})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range 2 {
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
			if _, err := io.CopyN(io.Discard, serverConn, int64(parsed.BodyLen)); err != nil {
				serverDone <- err
				return
			}
			ids <- parsed.PacketID
			reply, err := (loco.Packet{Header: loco.Header{
				PacketID: parsed.PacketID, Method: parsed.Method, BodyType: loco.BodyTypeBSON,
			}, Body: body}).MarshalBinary(0)
			if err != nil {
				serverDone <- err
				return
			}
			if _, err := serverConn.Write(reply); err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- nil
	}()

	for range 2 {
		if _, err := session.Request(context.Background(), "PING", []byte{5, 0, 0, 0, 0}); err != nil {
			t.Fatal(err)
		}
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	first, second := <-ids, <-ids
	if first != 199999999 || second != 100000000 {
		t.Fatalf("request IDs = %d, %d; want 199999999, 100000000", first, second)
	}
}

func TestSessionRequestIDSkipsPendingAfterWrap(t *testing.T) {
	session := &Session{
		nextID: 199999999,
		pending: map[uint32]chan requestResult{
			100000000: make(chan requestResult),
		},
	}
	session.mu.Lock()
	first, err := session.allocateRequestIDLocked()
	if err != nil {
		session.mu.Unlock()
		t.Fatal(err)
	}
	second, err := session.allocateRequestIDLocked()
	session.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if first != 199999999 || second != 100000001 {
		t.Fatalf("request IDs = %d, %d; want 199999999, 100000001", first, second)
	}
}

func TestSessionMalformedHeaderFailsPendingExactlyOnce(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	session := &Session{
		wire:    &wireConn{c: clientConn},
		pushes:  make(chan loco.Packet, 1),
		pending: make(map[uint32]chan requestResult),
	}
	waiter := make(chan requestResult, 1)
	session.pending[100000000] = waiter
	go session.readLoop()
	defer func() { _ = clientConn.Close(); _ = serverConn.Close() }()
	if _, err := serverConn.Write(make([]byte, loco.HeaderSize)); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-waiter:
		if result.err == nil {
			t.Fatal("malformed header completed pending request successfully")
		}
	case <-time.After(time.Second):
		t.Fatal("malformed header did not fail pending request")
	}
	select {
	case _, ok := <-session.pushes:
		if ok {
			t.Fatal("push stream yielded a value after malformed header")
		}
	case <-time.After(time.Second):
		t.Fatal("reader did not close push stream after malformed header")
	}
	select {
	case result := <-waiter:
		t.Fatalf("pending callback invoked twice: %#v", result)
	default:
	}
	if session.pending != nil {
		t.Fatalf("pending map = %#v, want nil", session.pending)
	}
}
