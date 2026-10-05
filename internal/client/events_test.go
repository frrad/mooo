package client

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDecodeEventStreamContinuesAfterMalformedPacket(t *testing.T) {
	raw := make(chan loco.Packet, 2)
	output := make(chan events.Result, 2)
	malformed, _ := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}})
	unknown, _ := bson.Marshal(bson.D{{Key: "status", Value: int32(0)}})
	raw <- loco.Packet{Header: loco.Header{Method: "MSG"}, Body: malformed}
	raw <- loco.Packet{Header: loco.Header{Method: "KICKOUT"}, Body: unknown}
	close(raw)
	decodeEventStream(raw, output)

	first := <-output
	if !errors.Is(first.Err, events.ErrMalformedEvent) || first.Event != nil {
		t.Fatalf("first result = %#v", first)
	}
	second := <-output
	kickout, ok := second.Event.(events.Kickout)
	if second.Err != nil || !ok || kickout.Reason != 0 {
		t.Fatalf("second result = %#v", second)
	}
	if _, ok := <-output; ok {
		t.Fatal("event output did not close")
	}
}

func TestMalformedTerminalNoticeDoesNotTriggerShutdown(t *testing.T) {
	raw := make(chan loco.Packet, 1)
	output := make(chan events.Result, 1)
	called := false
	raw <- loco.Packet{Header: loco.Header{Method: "CHANGESVR"}, Body: []byte{1, 2, 3}}
	close(raw)
	decodeEventStreamWithTerminal(raw, output, nil, nil, func() { called = true })
	result := <-output
	if !errors.Is(result.Err, events.ErrMalformedEvent) {
		t.Fatalf("malformed terminal error = %v", result.Err)
	}
	if called {
		t.Fatal("malformed terminal notice triggered shutdown")
	}
}

func TestTerminalNoticeClosesSessionEventStream(t *testing.T) {
	raw := make(chan loco.Packet, 2)
	session := &Session{pushes: raw}
	api := &Client{session: session}
	stream, err := api.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	empty, err := bson.Marshal(bson.D{})
	if err != nil {
		t.Fatal(err)
	}
	raw <- loco.Packet{Header: loco.Header{Method: "CHANGESVR"}, Body: empty}
	select {
	case result := <-stream:
		if result.Err != nil {
			t.Fatalf("CHANGESVR result error = %v", result.Err)
		}
		if _, ok := result.Event.(events.ChangeServer); !ok {
			t.Fatalf("CHANGESVR event = %T, want events.ChangeServer", result.Event)
		}
	case <-time.After(time.Second):
		t.Fatal("CHANGESVR was not delivered")
	}
	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("event stream remained open after terminal notice")
		}
	case <-time.After(time.Second):
		t.Fatal("event stream did not close after terminal notice")
	}
	if !session.closed {
		t.Fatal("terminal notice did not close the Session")
	}
	if _, err := api.SendText(context.Background(), 7, "after-terminal"); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("SendText after terminal error = %v, want %v", err, ErrClientClosed)
	}
}

func TestTerminalNoticeClosesOwnedCarriageAndRejectsSend(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()
	session := &Session{
		wire:    &wireConn{c: clientConn},
		pushes:  make(chan loco.Packet, 4),
		pending: make(map[uint32]chan requestResult),
	}
	api := &Client{session: session}
	stream, err := api.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	session.startReadLoop()
	empty, err := bson.Marshal(bson.D{})
	if err != nil {
		t.Fatal(err)
	}
	frame, err := (loco.Packet{Header: loco.Header{Method: "KICKOUT"}, Body: empty}).MarshalBinary(0)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = serverConn.Write(frame) }()
	select {
	case result := <-stream:
		if result.Err != nil {
			t.Fatalf("KICKOUT result error = %v", result.Err)
		}
		if _, ok := result.Event.(events.Kickout); !ok {
			t.Fatalf("KICKOUT event = %T, want events.Kickout", result.Event)
		}
	case <-time.After(time.Second):
		t.Fatal("KICKOUT was not delivered from carriage")
	}
	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("event stream yielded after terminal carriage close")
		}
	case <-time.After(time.Second):
		t.Fatal("event stream did not close after terminal carriage close")
	}
	if _, err := api.SendText(context.Background(), 7, "after-terminal"); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("SendText after terminal carriage close = %v, want %v", err, ErrClientClosed)
	}
}

func TestShutdownCancelsBlockedEventDecoder(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := continuity.Open(filepath.Join(dir, "checkpoint"))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := acquireProfileLease(filepath.Join(dir, "profile.lock"))
	if err != nil {
		t.Fatal(err)
	}
	clientConn, serverConn := net.Pipe()
	defer func() { _ = serverConn.Close() }()
	session := &Session{
		wire:    &wireConn{c: clientConn},
		pushes:  make(chan loco.Packet, requestLimit),
		pending: make(map[uint32]chan requestResult),
	}
	api := &Client{session: session, checkpoint: checkpoint, lease: lease}
	stream, err := api.Events(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	session.startReadLoop()
	go func() {
		for i := int64(1); i <= requestLimit+1; i++ {
			body, marshalErr := bson.Marshal(bson.D{
				{Key: "chatId", Value: int64(42)},
				{Key: "chatLog", Value: bson.D{{Key: "logId", Value: i}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "replay"}}},
			})
			if marshalErr != nil {
				return
			}
			frame, marshalErr := (loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body}).MarshalBinary(0)
			if marshalErr != nil {
				return
			}
			if _, writeErr := serverConn.Write(frame); writeErr != nil {
				return
			}
		}
	}()
	deadline := time.After(time.Second)
	for len(stream) < cap(stream) {
		select {
		case <-deadline:
			t.Fatalf("typed decoder did not fill output: len=%d cap=%d", len(stream), cap(stream))
		default:
			time.Sleep(time.Millisecond)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := api.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown with blocked decoder: %v", err)
	}
	if checkpoint.IsCommitted(42, 1) {
		t.Fatal("shutdown committed an undelivered event")
	}
	otherLease, err := acquireProfileLease(filepath.Join(dir, "profile.lock"))
	if err != nil {
		t.Fatalf("profile lease after decoder shutdown: %v", err)
	}
	_ = otherLease.Close()
}

func TestShutdownCancelsIdleEventDecoder(t *testing.T) {
	session := &Session{pushes: make(chan loco.Packet)}
	api := &Client{session: session}
	if _, err := api.Events(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := api.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown with idle decoder: %v", err)
	}
}

func TestDecodeEventStreamSuppressesCommittedAndInProcessDuplicates(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := continuity.Open(filepath.Join(dir, "checkpoint"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkpoint.CommitMessage(42, 9); err != nil {
		t.Fatal(err)
	}
	raw := make(chan loco.Packet, 4)
	output := make(chan events.Result, 4)
	message := func(logID int64, text string) loco.Packet {
		body, err := bson.Marshal(bson.D{
			{Key: "chatId", Value: int64(42)},
			{Key: "chatLog", Value: bson.D{{Key: "logId", Value: logID}, {Key: "type", Value: int32(1)}, {Key: "message", Value: text}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body}
	}
	raw <- message(9, "already committed")
	raw <- message(10, "new")
	raw <- message(10, "duplicate")
	close(raw)
	decodeEventStreamWithContinuity(raw, output, checkpoint, nil)

	result, ok := <-output
	if !ok || result.Err != nil {
		t.Fatalf("result = %#v", result)
	}
	messageEvent, ok := result.Event.(events.TextMessage)
	if !ok || messageEvent.LogID != 10 || messageEvent.Message != "new" {
		t.Fatalf("event = %#v", result.Event)
	}
	if _, ok := <-output; ok {
		t.Fatal("duplicate event was emitted")
	}
}

func TestRawPushConsumerPreventsTypedConsumer(t *testing.T) {
	state := reusableTestState()
	api, err := newClient(state, nil)
	if err != nil {
		t.Fatal(err)
	}
	pushes := make(chan loco.Packet)
	api.dial = func(context.Context, authstate.State) (*Session, error) {
		return &Session{pushes: pushes}, nil
	}
	if _, err := api.Pushes(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Events(t.Context()); !errors.Is(err, ErrPushConsumerSelected) {
		t.Fatalf("Events after Pushes error = %v", err)
	}
}

func TestRawPushConsumerLeavesTerminalShutdownToCaller(t *testing.T) {
	raw := make(chan loco.Packet, 1)
	session := &Session{pushes: raw}
	api := &Client{session: session}
	stream, err := api.Pushes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	empty, err := bson.Marshal(bson.D{})
	if err != nil {
		t.Fatal(err)
	}
	raw <- loco.Packet{Header: loco.Header{Method: "CHANGESVR"}, Body: empty}
	select {
	case packet := <-stream:
		if packet.Header.Method != "CHANGESVR" {
			t.Fatalf("raw terminal method = %q", packet.Header.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("raw terminal packet was not delivered")
	}
	if session.closed {
		t.Fatal("raw Pushes unexpectedly closed Session on terminal packet")
	}
	if err := api.Close(); err != nil {
		t.Fatal(err)
	}
}
