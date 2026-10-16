package client

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/loco"
)

// White-box tests over a raw net.Pipe, like the keepalive tests: they control
// when the peer replies and when the injected receive-header timer fires.

// headerTimeoutPeer reads request headers from the server end and reports
// each request's packet ID and method.
func headerTimeoutPeer(t *testing.T, serverConn net.Conn) <-chan loco.Header {
	t.Helper()
	headers := make(chan loco.Header, 4)
	go func() {
		for {
			raw := make([]byte, loco.HeaderSize)
			if _, err := io.ReadFull(serverConn, raw); err != nil {
				return
			}
			header, err := loco.ParseHeader(raw, 0)
			if err != nil {
				return
			}
			if _, err := io.CopyN(io.Discard, serverConn, int64(header.BodyLen)); err != nil {
				return
			}
			headers <- header
		}
	}()
	return headers
}

func writeStatusReply(t *testing.T, serverConn net.Conn, request loco.Header) {
	t.Helper()
	body, err := bson.Marshal(bson.D{{Key: "status", Value: int32(0)}})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := (loco.Packet{Header: loco.Header{PacketID: request.PacketID, Method: request.Method, BodyType: loco.BodyTypeBSON}, Body: body}).MarshalBinary(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serverConn.Write(packet); err != nil {
		t.Fatal(err)
	}
}

func receiveRequest(t *testing.T, headers <-chan loco.Header) loco.Header {
	t.Helper()
	select {
	case header := <-headers:
		return header
	case <-time.After(time.Second):
		t.Fatal("peer did not receive the request")
		return loco.Header{}
	}
}

func waitPushesClosed(t *testing.T, pushes <-chan loco.Packet) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-pushes:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("push stream did not close")
		}
	}
}

func liveHeaderTimers(clock *queuedTimerClock) int {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	live := 0
	for _, timer := range clock.timers {
		if !timer.stopped.Load() {
			live++
		}
	}
	return live
}

func TestSessionReceiveHeaderTimeoutClosesCarriageAndFailsPendingRequest(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	clock := &queuedTimerClock{}
	pushes := make(chan loco.Packet, 1)
	session := &session{
		wire:          &wireConn{c: clientConn},
		pushes:        pushes,
		pending:       make(map[uint32]pendingRequest),
		headerClock:   clock,
		headerTimeout: 20 * time.Second,
	}
	go session.readLoop()
	defer func() { _ = session.Close(); _ = serverConn.Close() }()
	headers := headerTimeoutPeer(t, serverConn)

	requestDone := make(chan error, 1)
	go func() {
		_, err := session.request(context.Background(), "GETMSGS", []byte{5, 0, 0, 0, 0})
		requestDone <- err
	}()
	receiveRequest(t, headers)
	waitForCondition(t, func() bool { return liveHeaderTimers(clock) == 1 })
	clock.mu.Lock()
	delay := clock.delays[0]
	clock.mu.Unlock()
	if delay != 20*time.Second {
		t.Fatalf("receive-header timer delay = %s, want 20s", delay)
	}

	// The peer never replies.
	clock.run(0)

	select {
	case err := <-requestDone:
		if !errors.Is(err, ErrClosed) || !errors.Is(err, errReceiveHeaderTimeout) {
			t.Fatalf("request error = %v, want ErrClosed from the receive-header timeout", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending request did not fail after the receive-header timeout")
	}
	waitPushesClosed(t, pushes)
}

func TestSessionReplyDisarmsReceiveHeaderTimeout(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	clock := &queuedTimerClock{}
	pushes := make(chan loco.Packet, 1)
	session := &session{
		wire:          &wireConn{c: clientConn},
		pushes:        pushes,
		pending:       make(map[uint32]pendingRequest),
		headerClock:   clock,
		headerTimeout: 20 * time.Second,
	}
	go session.readLoop()
	defer func() { _ = session.Close(); _ = serverConn.Close() }()
	headers := headerTimeoutPeer(t, serverConn)

	requestDone := make(chan error, 1)
	go func() {
		_, err := session.request(context.Background(), "GETMSGS", []byte{5, 0, 0, 0, 0})
		requestDone <- err
	}()
	writeStatusReply(t, serverConn, receiveRequest(t, headers))
	select {
	case err := <-requestDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("answered request did not return")
	}
	// A reply that arrives before the timer is armed leaves no timer at all;
	// either way, no armed timer may survive the reply.
	if live := liveHeaderTimers(clock); live != 0 {
		t.Fatalf("live receive-header timers after the reply = %d, want 0", live)
	}
	select {
	case _, ok := <-pushes:
		if !ok {
			t.Fatal("answered request closed the session")
		}
	default:
	}
}

// The timer does not belong to the caller: on the Mac it stays armed after the
// caller stops waiting, and only a reply or the timeout ends it.
func TestSessionAbandonedRequestKeepsReceiveHeaderTimeout(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	clock := &queuedTimerClock{}
	pushes := make(chan loco.Packet, 1)
	session := &session{
		wire:          &wireConn{c: clientConn},
		pushes:        pushes,
		pending:       make(map[uint32]pendingRequest),
		headerClock:   clock,
		headerTimeout: 20 * time.Second,
	}
	go session.readLoop()
	defer func() { _ = session.Close(); _ = serverConn.Close() }()
	headers := headerTimeoutPeer(t, serverConn)

	ctx, cancel := context.WithCancel(context.Background())
	requestDone := make(chan error, 1)
	go func() {
		_, err := session.request(ctx, "GETMSGS", []byte{5, 0, 0, 0, 0})
		requestDone <- err
	}()
	receiveRequest(t, headers)
	waitForCondition(t, func() bool { return liveHeaderTimers(clock) == 1 })
	cancel()
	if err := <-requestDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("request error = %v, want cancellation", err)
	}
	if live := liveHeaderTimers(clock); live != 1 {
		t.Fatalf("live receive-header timers after abandonment = %d, want 1", live)
	}

	clock.run(0)
	waitPushesClosed(t, pushes)
}

func TestSessionLateReplyToAbandonedRequestDisarmsReceiveHeaderTimeout(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	clock := &queuedTimerClock{}
	pushes := make(chan loco.Packet, 1)
	session := &session{
		wire:          &wireConn{c: clientConn},
		pushes:        pushes,
		pending:       make(map[uint32]pendingRequest),
		headerClock:   clock,
		headerTimeout: 20 * time.Second,
	}
	go session.readLoop()
	defer func() { _ = session.Close(); _ = serverConn.Close() }()
	headers := headerTimeoutPeer(t, serverConn)

	ctx, cancel := context.WithCancel(context.Background())
	requestDone := make(chan error, 1)
	go func() {
		_, err := session.request(ctx, "GETMSGS", []byte{5, 0, 0, 0, 0})
		requestDone <- err
	}()
	request := receiveRequest(t, headers)
	waitForCondition(t, func() bool { return liveHeaderTimers(clock) == 1 })
	cancel()
	if err := <-requestDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("abandoned request error = %v, want context.Canceled", err)
	}

	writeStatusReply(t, serverConn, request)
	waitForCondition(t, func() bool { return liveHeaderTimers(clock) == 0 })
	clock.run(0)
	select {
	case _, ok := <-pushes:
		if !ok {
			t.Fatal("a stopped receive-header timer closed the session")
		}
	case <-time.After(50 * time.Millisecond):
	}
}

func waitForCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
