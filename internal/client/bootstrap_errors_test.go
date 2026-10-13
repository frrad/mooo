package client

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/booking"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// A failed bootstrap names its stage and keeps its network cause reachable
// through errors.As, so the connector retries it. Bootstrap runs before
// LOGINLIST and sends no credentials. Regression: a DNS failure right after
// the Mac woke stopped the lab bridge for good (2026-10-11).
func TestConnectSessionGetConfDNSFailureExposesNetworkCause(t *testing.T) {
	notFound := &net.DNSError{Err: "no such host", Name: booking.Host, IsNotFound: true}
	dialers := sessionDialers{
		tls: func(context.Context, string, int) (*wireConn, error) {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: notFound}
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
	if !strings.Contains(err.Error(), "getconf dial") || !strings.Contains(err.Error(), "no such host") {
		t.Fatalf("connectSession error = %q, want the stage and the cause text", err)
	}
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		t.Fatalf("connectSession error hides its DNS cause: %v", err)
	}
}

func TestConnectSessionGetConfDialFailureNamesStageAndExposesCause(t *testing.T) {
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
	if !errors.As(err, &networkErr) {
		t.Fatalf("connectSession error hides its network cause: %v", err)
	}
}

func TestConnectSessionCheckinFailureReportsLastAttempt(t *testing.T) {
	bookingBackend := newScriptedBackend(t, false, testloco.Expect("GETCONF", nil, statusDocument(
		bson.E{Key: "ticket", Value: bson.D{{Key: "lsl", Value: bson.A{"checkin.invalid"}}}},
		bson.E{Key: "wifi", Value: bson.D{{Key: "ports", Value: bson.A{int32(443)}}}},
	)))
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("synthetic checkin refused")}
	dialers := sessionDialers{
		tls: func(_ context.Context, host string, _ int) (*wireConn, error) {
			if host == booking.Host {
				return clientWire(bookingBackend), nil
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
	if !errors.As(err, &networkErr) {
		t.Fatalf("connectSession error hides its network cause: %v", err)
	}
}

// A transport failure during LOGINLIST stays classified only as ErrLogin. The
// request carried credentials, so the connector must not retry it as a
// transport error.
func TestConnectSessionLoginListDropHidesTransportCause(t *testing.T) {
	var loginRequests int
	dialers, backends := scriptedLogin(t, testloco.DropAfter("LOGINLIST", &loginRequests))

	_, err := connectSession(t.Context(), reusableTestState(), sessionConfig{
		resume:  continuity.Checkpoint{Version: continuity.Version},
		dialers: dialers,
	})
	waitBackends(t, backends...)

	if loginRequests != 1 {
		t.Fatalf("LOGINLIST requests = %d, want 1", loginRequests)
	}
	if !errors.Is(err, ErrLogin) {
		t.Fatalf("connectSession error = %v, want ErrLogin", err)
	}
	if !strings.Contains(err.Error(), "loginlist") {
		t.Fatalf("connectSession error = %q, want the loginlist stage", err)
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) || errors.Is(err, io.EOF) || errors.Is(err, ErrClosed) {
		t.Fatalf("connectSession error exposes a retryable transport cause: %v", err)
	}
}
