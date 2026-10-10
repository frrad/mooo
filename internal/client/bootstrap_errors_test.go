package client

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/booking"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// A failed bootstrap names its stage and keeps the cause's text, but stays
// classified only as errBootstrap: the network cause is not reachable through
// errors.As, so the connector does not start treating it as a retryable
// transport error.
func TestConnectSessionGetConfDialFailureNamesStageWithoutExposingCause(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("synthetic connection refused")}
	dialers := sessionDialers{
		tls: func(context.Context, string, int) (*wireConn, error) {
			return nil, refused
		},
		secure: func(context.Context, string, int) (*wireConn, error) {
			t.Fatal("secure dial attempted after GETCONF failed")
			return nil, nil
		},
	}

	_, err := connectSession(t.Context(), reusableTestState(), sessionConfig{
		resume:  continuity.Checkpoint{Version: continuity.Version},
		dialers: dialers,
	})

	if !errors.Is(err, errBootstrap) {
		t.Fatalf("connectSession error = %v, want errBootstrap", err)
	}
	if !strings.Contains(err.Error(), "getconf dial") || !strings.Contains(err.Error(), "synthetic connection refused") {
		t.Fatalf("connectSession error = %q, want the stage and the cause text", err)
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		t.Fatalf("connectSession error exposes its network cause: %v", err)
	}
}

func TestConnectSessionCheckinFailureReportsLastAttempt(t *testing.T) {
	bookingBackend := newScriptedBackend(t, false, expectRequest("GETCONF", nil, statusDocument(
		bson.E{Key: "ticket", Value: bson.D{{Key: "lsl", Value: bson.A{"checkin.invalid"}}}},
		bson.E{Key: "wifi", Value: bson.D{{Key: "ports", Value: bson.A{int32(443)}}}},
	)))
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("synthetic checkin refused")}
	dialers := sessionDialers{
		tls: func(_ context.Context, host string, _ int) (*wireConn, error) {
			if host == booking.Host {
				return bookingBackend.client, nil
			}
			return nil, refused
		},
		secure: func(context.Context, string, int) (*wireConn, error) {
			return nil, refused
		},
	}

	_, err := connectSession(t.Context(), reusableTestState(), sessionConfig{
		resume:  continuity.Checkpoint{Version: continuity.Version},
		dialers: dialers,
	})

	if !errors.Is(err, errBootstrap) {
		t.Fatalf("connectSession error = %v, want errBootstrap", err)
	}
	if !strings.Contains(err.Error(), "checkin dial") || !strings.Contains(err.Error(), "synthetic checkin refused") {
		t.Fatalf("connectSession error = %q, want the checkin dial stage and the cause text", err)
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		t.Fatalf("connectSession error exposes its network cause: %v", err)
	}
}
