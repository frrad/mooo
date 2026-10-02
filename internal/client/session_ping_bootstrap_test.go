package client

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/continuity"
	"go.mongodb.org/mongo-driver/v2/bson"
)

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
	clock.run(0)
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
	clock.run(0)
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
	if timerCount != 1 {
		t.Fatalf("timer count after failed PING=%d, want 1", timerCount)
	}
	close(release)
	for _, backend := range []*scriptedBackend{booking, checkin, carriage} {
		backend.wait(t)
	}
}
