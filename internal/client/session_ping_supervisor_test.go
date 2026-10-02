package client

import (
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type recordingPingScheduler struct {
	mu     sync.Mutex
	events []string
}

type blockingPingScheduler struct {
	recordingPingScheduler
	entered chan struct{}
	release chan struct{}
}

type closeAdmissionScheduler struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (s *closeAdmissionScheduler) queueCancel() {
	if s.calls.Add(1) == 1 {
		close(s.entered)
		<-s.release
	}
}

func (*closeAdmissionScheduler) queueSchedule() bool { return true }

type closeAdmissionConn struct {
	net.Conn
	writes atomic.Int32
}

func (c *closeAdmissionConn) Write([]byte) (int, error) {
	c.writes.Add(1)
	return 0, errors.New("synthetic write during close")
}

func (*closeAdmissionConn) Close() error { return nil }

func (s *blockingPingScheduler) queueSchedule() bool {
	s.mu.Lock()
	s.events = append(s.events, "schedule")
	s.mu.Unlock()
	close(s.entered)
	<-s.release
	return true
}

func TestSessionLifecycleSchedulerBindsBeforeUse(t *testing.T) {
	scheduler := &recordingPingScheduler{}
	session := newSession(scheduler)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if got, want := scheduler.snapshot(), []string{"cancel"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scheduler events=%v want=%v", got, want)
	}
}

func TestSessionLifecycleSchedulerCannotBeReplaced(t *testing.T) {
	session := newSession(&recordingPingScheduler{})
	if err := session.setLifecycleScheduler(&recordingPingScheduler{}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("rebinding error=%v want ErrProtocol", err)
	}
}

func TestSessionRejectsRequestsDuringLocalClose(t *testing.T) {
	scheduler := &closeAdmissionScheduler{entered: make(chan struct{}), release: make(chan struct{})}
	conn := &closeAdmissionConn{}
	session := &Session{
		wire:               &wireConn{c: conn},
		pending:            make(map[uint32]chan requestResult),
		lifecycleScheduler: scheduler,
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- session.Close() }()
	defer func() {
		close(scheduler.release)
		select {
		case err := <-closeDone:
			if err != nil {
				t.Errorf("Close: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("Close did not finish")
		}
	}()
	select {
	case <-scheduler.entered:
	case <-time.After(time.Second):
		t.Fatal("Close did not reach owner cancellation")
	}
	_, err := session.Request(context.Background(), "NOTIREAD", []byte{5, 0, 0, 0, 0})
	if !errors.Is(err, ErrClosed) {
		t.Errorf("request during Close: %v; want ErrClosed", err)
	}
	if writes := conn.writes.Load(); writes != 0 {
		t.Errorf("request during Close wrote %d packets; want zero", writes)
	}
}

func TestSessionCloseWaitsForSelectedCompletionBeforeCancelling(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	scheduler := &blockingPingScheduler{entered: make(chan struct{}), release: make(chan struct{})}
	waiter := make(chan requestResult, 1)
	session := &Session{
		wire:               &wireConn{c: clientConn},
		pushes:             make(chan loco.Packet, 1),
		pending:            map[uint32]chan requestResult{100000000: waiter},
		lifecycleScheduler: scheduler,
	}
	go session.readLoop()
	defer func() { _ = clientConn.Close(); _ = serverConn.Close() }()
	body, err := bson.Marshal(bson.D{{Key: "status", Value: int32(0)}})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := (loco.Packet{Header: loco.Header{PacketID: 100000000, Method: "PING", BodyType: loco.BodyTypeBSON}, Body: body}).MarshalBinary(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serverConn.Write(packet); err != nil {
		t.Fatal(err)
	}
	select {
	case <-scheduler.entered:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not observe selected completion")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- session.Close() }()
	select {
	case <-closeDone:
		t.Fatal("close completed before selected completion scheduling")
	case <-time.After(20 * time.Millisecond):
	}
	close(scheduler.release)
	select {
	case result := <-waiter:
		if result.err != nil {
			t.Fatal(result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("selected completion was not delivered")
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if got, want := scheduler.snapshot(), []string{"schedule", "cancel"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scheduler events=%v want=%v", got, want)
	}
}

func (s *recordingPingScheduler) queueCancel() {
	s.mu.Lock()
	s.events = append(s.events, "cancel")
	s.mu.Unlock()
}

func (s *recordingPingScheduler) queueSchedule() bool {
	s.mu.Lock()
	s.events = append(s.events, "schedule")
	s.mu.Unlock()
	return true
}

func (s *recordingPingScheduler) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

func TestSessionPingTransportCompletionSchedulesBeforePendingDelivery(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	scheduler := &recordingPingScheduler{}
	waiter := make(chan requestResult, 1)
	session := &Session{
		wire:               &wireConn{c: clientConn},
		pushes:             make(chan loco.Packet, 1),
		pending:            map[uint32]chan requestResult{100000000: waiter},
		lifecycleScheduler: scheduler,
	}
	go session.readLoop()
	defer func() { _ = clientConn.Close(); _ = serverConn.Close() }()

	body, err := bson.Marshal(bson.D{{Key: "status", Value: int32(0)}})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := (loco.Packet{Header: loco.Header{PacketID: 100000000, Method: "PING", BodyType: loco.BodyTypeBSON}, Body: body}).MarshalBinary(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serverConn.Write(packet); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-waiter:
		if result.err != nil {
			t.Fatal(result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("PING waiter was not delivered")
	}
	if got, want := scheduler.snapshot(), []string{"schedule"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scheduler events=%v want=%v", got, want)
	}
}

func TestSessionPingContextCancellationDoesNotScheduleRearm(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	scheduler := &recordingPingScheduler{}
	session := &Session{
		wire:               &wireConn{c: clientConn},
		pushes:             make(chan loco.Packet, 1),
		pending:            make(map[uint32]chan requestResult),
		lifecycleScheduler: scheduler,
	}
	go session.readLoop()
	defer func() { _ = session.Close(); _ = serverConn.Close() }()

	requestRead := make(chan struct{})
	go func() {
		header := make([]byte, loco.HeaderSize)
		if _, err := io.ReadFull(serverConn, header); err != nil {
			return
		}
		parsed, err := loco.ParseHeader(header, 0)
		if err == nil {
			_, _ = io.CopyN(io.Discard, serverConn, int64(parsed.BodyLen))
		}
		close(requestRead)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	requestDone := make(chan error, 1)
	go func() {
		_, err := session.Request(ctx, "PING", []byte{5, 0, 0, 0, 0})
		requestDone <- err
	}()
	select {
	case <-requestRead:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("server did not receive PING")
	}
	select {
	case err := <-requestDone:
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("request error=%v want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled PING did not return")
	}
	if got, want := scheduler.snapshot(), []string{"cancel"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scheduler events=%v want=%v", got, want)
	}
}

func TestSessionNonPingTransportCompletionSchedulesRearm(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	scheduler := &recordingPingScheduler{}
	waiter := make(chan requestResult, 1)
	session := &Session{
		wire:               &wireConn{c: clientConn},
		pushes:             make(chan loco.Packet, 1),
		pending:            map[uint32]chan requestResult{100000000: waiter},
		lifecycleScheduler: scheduler,
	}
	go session.readLoop()
	defer func() { _ = clientConn.Close(); _ = serverConn.Close() }()
	body, err := bson.Marshal(bson.D{{Key: "status", Value: int32(0)}})
	if err != nil {
		t.Fatal(err)
	}
	packet, err := (loco.Packet{Header: loco.Header{PacketID: 100000000, Method: "NOTIREAD", BodyType: loco.BodyTypeBSON}, Body: body}).MarshalBinary(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := serverConn.Write(packet); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-waiter:
		if result.err != nil {
			t.Fatal(result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("ordinary waiter was not delivered")
	}
	if got, want := scheduler.snapshot(), []string{"schedule"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scheduler events=%v want=%v", got, want)
	}
}

func TestSessionOrdinaryWriteFailureDoesNotScheduleRearm(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	if err := serverConn.Close(); err != nil {
		t.Fatal(err)
	}
	scheduler := &recordingPingScheduler{}
	session := &Session{
		wire:               &wireConn{c: clientConn},
		pushes:             make(chan loco.Packet, 1),
		pending:            make(map[uint32]chan requestResult),
		lifecycleScheduler: scheduler,
	}
	defer func() { _ = clientConn.Close() }()
	if _, err := session.Request(context.Background(), "NOTIREAD", []byte{5, 0, 0, 0, 0}); err == nil {
		t.Fatal("ordinary request unexpectedly succeeded after write failure")
	}
	if got, want := scheduler.snapshot(), []string{"cancel"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scheduler events=%v want=%v", got, want)
	}
}

func TestSessionTransportDisconnectSchedulesBeforeTerminalFanout(t *testing.T) {
	scheduler := &recordingPingScheduler{}
	waiter := make(chan requestResult, 1)
	session := &Session{
		pushes:             make(chan loco.Packet, 1),
		pending:            map[uint32]chan requestResult{100000000: waiter},
		lifecycleScheduler: scheduler,
	}
	wantErr := errors.New("synthetic carriage disconnect")
	session.finishRead(wantErr)
	if got, want := scheduler.snapshot(), []string{"schedule"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scheduler events=%v want=%v", got, want)
	}
	select {
	case result := <-waiter:
		if !errors.Is(result.err, wantErr) {
			t.Fatalf("terminal error=%v want=%v", result.err, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatal("pending waiter was not failed")
	}
}

func TestSessionCloseCancelsWithoutTerminalRearm(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	scheduler := &recordingPingScheduler{}
	waiter := make(chan requestResult, 1)
	session := &Session{
		wire:               &wireConn{c: clientConn},
		pushes:             make(chan loco.Packet, 1),
		pending:            map[uint32]chan requestResult{100000000: waiter},
		lifecycleScheduler: scheduler,
	}
	go session.readLoop()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverConn.Close() }()
	select {
	case result := <-waiter:
		if result.err == nil {
			t.Fatal("closed session completed pending request successfully")
		}
	case <-time.After(time.Second):
		t.Fatal("close did not fail pending waiter")
	}
	if got, want := scheduler.snapshot(), []string{"cancel"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scheduler events=%v want=%v", got, want)
	}
}
