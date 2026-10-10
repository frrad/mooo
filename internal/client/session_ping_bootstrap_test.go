package client

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/loco"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestSessionFinishBootstrapPreservesBufferedPushesAndClosedState(t *testing.T) {
	session := newSession(nil)
	session.bootstrapDone = false
	session.bootstrapPushes = make([]loco.Packet, 70)
	for i := range session.bootstrapPushes {
		session.bootstrapPushes[i].Header.PacketID = uint32(i + 1)
	}
	if !session.finishBootstrap() {
		t.Fatal("finishBootstrap returned false for an open session")
	}
	if got := len(session.pushes); got != 70 {
		t.Fatalf("buffered push count = %d, want 70", got)
	}
	for want := 1; want <= 70; want++ {
		select {
		case packet := <-session.pushes:
			if packet.Header.PacketID != uint32(want) {
				t.Fatalf("buffered push %d has packet ID %d", want, packet.Header.PacketID)
			}
		default:
			t.Fatalf("buffered push %d was not transferred", want)
		}
	}

	closed := newSession(nil)
	closed.bootstrapDone = false
	closed.bootstrapPushes = []loco.Packet{{Header: loco.Header{PacketID: 9}}}
	closed.closing = true
	if closed.finishBootstrap() {
		t.Fatal("finishBootstrap accepted a closing session")
	}
	if closed.bootstrapPushes == nil || len(closed.bootstrapPushes) != 1 {
		t.Fatal("closing session lost its buffered pushes")
	}
}

func TestSessionExplicitRequestIDRejectsPendingCollision(t *testing.T) {
	waiter := make(chan requestResult, 1)
	session := newSession(nil)
	session.wire = &wireConn{}
	session.nextID = 2
	session.pending[2] = pendingRequest{method: "PING", result: waiter}
	_, err := session.requestRaw(context.Background(), 2, "LCHATLIST", nil)
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("requestRaw collision error = %v, want %v", err, ErrProtocol)
	}
	if got := session.pending[2]; got.result != waiter {
		t.Fatal("requestRaw collision replaced the existing waiter")
	}
}

func TestConnectSessionArmsPingOnlyAfterLoginCompletion(t *testing.T) {
	state := reusableTestState()
	dialers, backends := scriptedLogin(t,
		testloco.Expect("LOGINLIST", nil, statusDocument(
			bson.E{Key: "chatDatas", Value: bson.A{}},
			bson.E{Key: "eof", Value: true},
			bson.E{Key: "lastTokenId", Value: int64(11)},
			bson.E{Key: "lbk", Value: int32(2)},
		)),
		testloco.Expect("PING", func(raw bson.Raw) error {
			if err := requireExactKeys(raw); err != nil {
				return fmt.Errorf("PING body: %w", err)
			}
			return nil
		}, statusDocument(bson.E{Key: "status", Value: int32(0)})),
	)
	clock := &queuedTimerClock{}
	session, err := connectSession(t.Context(), state, sessionConfig{resume: continuity.Checkpoint{Version: continuity.Version}, dialers: dialers, ping: pingSessionOptions{clock: clock, interval: time.Second, timeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	clock.runLast()
	waitBackends(t, backends...)
}

func TestConnectSessionPingTimeoutDoesNotKeepRequestPending(t *testing.T) {
	state := reusableTestState()
	pingRead := make(chan struct{})
	release := make(chan struct{})
	dialers, backends := scriptedLogin(t,
		testloco.Expect("LOGINLIST", nil, statusDocument(
			bson.E{Key: "eof", Value: true},
			bson.E{Key: "lastTokenId", Value: int64(11)},
			bson.E{Key: "lbk", Value: int32(2)},
		)),
		func(server *testloco.Peer) error {
			request, err := server.Read()
			if err != nil {
				return err
			}
			if request.Header.Method != "PING" {
				return fmt.Errorf("method = %q, want PING", request.Header.Method)
			}
			close(pingRead)
			<-release
			return nil
		},
	)
	clock := &queuedTimerClock{}
	session, err := connectSession(t.Context(), state, sessionConfig{resume: continuity.Checkpoint{Version: continuity.Version}, dialers: dialers, ping: pingSessionOptions{clock: clock, interval: time.Second, timeout: 20 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	clock.mu.Lock()
	timersBefore := len(clock.timers)
	clock.mu.Unlock()
	clock.runLast()
	select {
	case <-pingRead:
	case <-time.After(time.Second):
		t.Fatal("PING request was not written")
	}
	// The timeout closes the ambiguous carriage through the normal terminal
	// path, without leaving a pending request or rearming the owner.
	deadline := time.After(time.Second)
	for {
		session.mu.Lock()
		closed := session.closed
		pending := len(session.pending)
		session.mu.Unlock()
		if closed {
			if pending != 0 {
				t.Fatalf("pending callbacks after timeout=%d, want 0", pending)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout did not close session")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	clock.mu.Lock()
	timerCount := len(clock.timers)
	clock.mu.Unlock()
	if timerCount != timersBefore {
		t.Fatalf("timer count after failed PING=%d, want unchanged at %d", timerCount, timersBefore)
	}
	close(release)
	waitBackends(t, backends...)
}

func TestConnectSessionHeartbeatCanCompleteDuringBootstrapPagination(t *testing.T) {
	state := reusableTestState()
	gapReady := make(chan struct{})
	allowLchat := make(chan struct{})
	keepOpen := make(chan struct{})
	pingSeen := make(chan struct{})
	dialers, backends := scriptedLogin(t,
		testloco.Expect("LOGINLIST", nil, statusDocument(
			bson.E{Key: "eof", Value: false},
			bson.E{Key: "lastTokenId", Value: int64(11)},
			bson.E{Key: "lastChatId", Value: int64(7)},
		)),
		func(server *testloco.Peer) error {
			for i := 0; i < 70; i++ {
				if err := server.WriteBSON(uint32(1000+i), "PUSH", bson.D{{Key: "index", Value: int32(i)}}); err != nil {
					return err
				}
			}
			ping, err := server.Read()
			if err != nil {
				return err
			}
			if ping.Header.Method != "PING" {
				return fmt.Errorf("method = %q, want PING before pagination request", ping.Header.Method)
			}
			if err := requireExactKeys(bson.Raw(ping.Body)); err != nil {
				return fmt.Errorf("PING body: %w", err)
			}
			close(pingSeen)
			if err := server.WriteBSON(ping.Header.PacketID, "PING", bson.D{{Key: "status", Value: int32(0)}}); err != nil {
				return err
			}
			lchat, err := server.Read()
			if err != nil {
				return err
			}
			if lchat.Header.Method != "LCHATLIST" {
				return fmt.Errorf("method = %q, want LCHATLIST", lchat.Header.Method)
			}
			if err := server.WriteBSON(lchat.Header.PacketID, "LCHATLIST", statusDocument(
				bson.E{Key: "eof", Value: true},
			)); err != nil {
				return err
			}
			<-keepOpen
			return nil
		},
	)
	clock := &queuedTimerClock{}
	result := make(chan struct {
		session *session
		err     error
	}, 1)
	go func() {
		connected, err := connectSession(t.Context(), state, sessionConfig{resume: continuity.Checkpoint{Version: continuity.Version}, dialers: dialers, ping: pingSessionOptions{clock: clock, interval: time.Second, timeout: time.Second, beforeBootstrapRequest: func() { close(gapReady); <-allowLchat }}})
		result <- struct {
			session *session
			err     error
		}{session: connected, err: err}
	}()
	select {
	case <-gapReady:
	case <-time.After(time.Second):
		t.Fatal("bootstrap did not reach the response-consumer gap")
	}
	clock.mu.Lock()
	timerIndex := len(clock.timers) - 1
	clock.mu.Unlock()
	clock.runEvenIfStopped(timerIndex)
	select {
	case <-pingSeen:
	case <-time.After(time.Second):
		t.Fatal("heartbeat was not admitted during pagination")
	}
	close(allowLchat)
	select {
	case outcome := <-result:
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		defer func() { _ = outcome.session.Close() }()
		for want := 0; want < 70; want++ {
			select {
			case packet := <-outcome.session.pushStream():
				if packet.Header.Method != "PUSH" {
					t.Fatalf("buffered push %d method = %q, want PUSH", want, packet.Header.Method)
				}
			case <-time.After(time.Second):
				t.Fatalf("buffered push %d was not delivered", want)
			}
		}
		close(keepOpen)
	case <-time.After(time.Second):
		t.Fatal("bootstrap did not complete after pagination heartbeat")
	}
	waitBackends(t, backends...)
}
