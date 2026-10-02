package client

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/loco"
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
	session.pending[2] = waiter
	_, err := session.requestRaw(context.Background(), 2, "LCHATLIST", nil)
	if err != ErrProtocol {
		t.Fatalf("requestRaw collision error = %v, want %v", err, ErrProtocol)
	}
	if got := session.pending[2]; got != waiter {
		t.Fatal("requestRaw collision replaced the existing waiter")
	}
}

func TestConnectSessionArmsPingOnlyAfterLoginCompletion(t *testing.T) {
	state := reusableTestState()
	booking := newScriptedBackend(t, false, expectRequest("GETCONF", nil, statusDocument(
		bson.E{Key: "ticket", Value: bson.D{{Key: "lsl", Value: bson.A{"checkin.invalid"}}}},
		bson.E{Key: "wifi", Value: bson.D{{Key: "ports", Value: bson.A{int32(443)}}}},
	)))
	checkin := newScriptedBackend(t, false, expectRequest("CHECKIN", nil, statusDocument(
		bson.E{Key: "host", Value: "carriage.invalid"}, bson.E{Key: "port", Value: int32(995)},
	)))
	carriage := newScriptedBackend(t, true,
		expectRequest("LOGINLIST", nil, statusDocument(
			bson.E{Key: "chatDatas", Value: bson.A{}},
			bson.E{Key: "eof", Value: true},
			bson.E{Key: "lastTokenId", Value: int64(11)},
			bson.E{Key: "lbk", Value: int32(2)},
		)),
		expectRequest("PING", func(raw bson.Raw) error {
			if err := requireExactKeys(raw); err != nil {
				return fmt.Errorf("PING body: %w", err)
			}
			return nil
		}, statusDocument(bson.E{Key: "status", Value: int32(0)})),
	)
	dialers := sessionDialers{
		tls: func(_ context.Context, host string, _ int) (*wireConn, error) {
			switch host {
			case bookingHost:
				return booking.client, nil
			case "checkin.invalid":
				return checkin.client, nil
			default:
				return nil, fmt.Errorf("unexpected TLS host %q", host)
			}
		},
		secure: func(_ context.Context, host string, _ int) (*wireConn, error) {
			if host != "carriage.invalid" {
				return nil, fmt.Errorf("unexpected secure host %q", host)
			}
			return carriage.client, nil
		},
	}
	clock := &queuedTimerClock{}
	session, err := connectSessionWithResumeOptions(t.Context(), state, continuity.Checkpoint{Version: continuity.Version}, dialers, pingSessionOptions{clock: clock, interval: time.Second, timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	clock.runLast()
	for _, backend := range []*scriptedBackend{booking, checkin, carriage} {
		backend.wait(t)
	}
}

func TestConnectSessionPingTimeoutDoesNotKeepRequestPending(t *testing.T) {
	state := reusableTestState()
	booking := newScriptedBackend(t, false, expectRequest("GETCONF", nil, statusDocument(
		bson.E{Key: "ticket", Value: bson.D{{Key: "lsl", Value: bson.A{"checkin.invalid"}}}},
		bson.E{Key: "wifi", Value: bson.D{{Key: "ports", Value: bson.A{int32(443)}}}},
	)))
	checkin := newScriptedBackend(t, false, expectRequest("CHECKIN", nil, statusDocument(
		bson.E{Key: "host", Value: "carriage.invalid"}, bson.E{Key: "port", Value: int32(995)},
	)))
	pingRead := make(chan struct{})
	release := make(chan struct{})
	carriage := newScriptedBackend(t, true,
		expectRequest("LOGINLIST", nil, statusDocument(
			bson.E{Key: "eof", Value: true},
			bson.E{Key: "lastTokenId", Value: int64(11)},
			bson.E{Key: "lbk", Value: int32(2)},
		)),
		func(server *wireConn) error {
			request, err := server.read()
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
	dialers := sessionDialers{
		tls: func(_ context.Context, host string, _ int) (*wireConn, error) {
			switch host {
			case bookingHost:
				return booking.client, nil
			case "checkin.invalid":
				return checkin.client, nil
			default:
				return nil, fmt.Errorf("unexpected TLS host %q", host)
			}
		},
		secure: func(_ context.Context, host string, _ int) (*wireConn, error) {
			if host != "carriage.invalid" {
				return nil, fmt.Errorf("unexpected secure host %q", host)
			}
			return carriage.client, nil
		},
	}
	clock := &queuedTimerClock{}
	session, err := connectSessionWithResumeOptions(t.Context(), state, continuity.Checkpoint{Version: continuity.Version}, dialers, pingSessionOptions{clock: clock, interval: time.Second, timeout: 20 * time.Millisecond})
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
	for _, backend := range []*scriptedBackend{booking, checkin, carriage} {
		backend.wait(t)
	}
}

func TestConnectSessionHeartbeatCanCompleteDuringBootstrapPagination(t *testing.T) {
	state := reusableTestState()
	booking := newScriptedBackend(t, false, expectRequest("GETCONF", nil, statusDocument(
		bson.E{Key: "ticket", Value: bson.D{{Key: "lsl", Value: bson.A{"checkin.invalid"}}}},
		bson.E{Key: "wifi", Value: bson.D{{Key: "ports", Value: bson.A{int32(443)}}}},
	)))
	checkin := newScriptedBackend(t, false, expectRequest("CHECKIN", nil, statusDocument(
		bson.E{Key: "host", Value: "carriage.invalid"}, bson.E{Key: "port", Value: int32(995)},
	)))
	gapReady := make(chan struct{})
	allowLchat := make(chan struct{})
	keepOpen := make(chan struct{})
	pingSeen := make(chan struct{})
	carriage := newScriptedBackend(t, true,
		expectRequest("LOGINLIST", nil, statusDocument(
			bson.E{Key: "eof", Value: false},
			bson.E{Key: "lastTokenId", Value: int64(11)},
			bson.E{Key: "lastChatId", Value: int64(7)},
		)),
		func(server *wireConn) error {
			for i := 0; i < 70; i++ {
				if err := writeBackendPacket(server, uint32(1000+i), "PUSH", mustBSON(bson.D{{Key: "index", Value: int32(i)}})); err != nil {
					return err
				}
			}
			ping, err := server.read()
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
			if err := writeBackendPacket(server, ping.Header.PacketID, "PING", mustBSON(bson.D{{Key: "status", Value: int32(0)}})); err != nil {
				return err
			}
			lchat, err := server.read()
			if err != nil {
				return err
			}
			if lchat.Header.Method != "LCHATLIST" {
				return fmt.Errorf("method = %q, want LCHATLIST", lchat.Header.Method)
			}
			if err := writeBackendPacket(server, lchat.Header.PacketID, "LCHATLIST", mustBSON(statusDocument(
				bson.E{Key: "eof", Value: true},
			))); err != nil {
				return err
			}
			<-keepOpen
			return nil
		},
	)
	dialers := sessionDialers{
		tls: func(_ context.Context, host string, _ int) (*wireConn, error) {
			switch host {
			case bookingHost:
				return booking.client, nil
			case "checkin.invalid":
				return checkin.client, nil
			default:
				return nil, fmt.Errorf("unexpected TLS host %q", host)
			}
		},
		secure: func(_ context.Context, host string, _ int) (*wireConn, error) {
			if host != "carriage.invalid" {
				return nil, fmt.Errorf("unexpected secure host %q", host)
			}
			return carriage.client, nil
		},
	}
	clock := &queuedTimerClock{}
	result := make(chan struct {
		session *Session
		err     error
	}, 1)
	go func() {
		session, err := connectSessionWithResumeOptions(t.Context(), state, continuity.Checkpoint{Version: continuity.Version}, dialers, pingSessionOptions{clock: clock, interval: time.Second, timeout: time.Second, beforeBootstrapRequest: func() { close(gapReady); <-allowLchat }})
		result <- struct {
			session *Session
			err     error
		}{session: session, err: err}
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
			case packet := <-outcome.session.Pushes():
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
	for _, backend := range []*scriptedBackend{booking, checkin, carriage} {
		backend.wait(t)
	}
}
