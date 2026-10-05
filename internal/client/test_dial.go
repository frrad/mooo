package client

import (
	"context"
	"net"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/friends"
	"github.com/frrad/mooo/internal/protocol/loco"
)

// TestConnection describes one synthetic LOCO transport endpoint. It is kept
// in the internal package so integration tests can exercise Client without
// exposing wireConn or the production dialers.
type TestConnection struct {
	Conn   net.Conn
	Secure *loco.SecureV3
}

// TestDialers injects already-established synthetic booking, check-in, and
// carriage transports. The connector integration tests use this seam to run
// the real bootstrap and session lifecycle against a scripted backend.
type TestDialers struct {
	TLS    func(context.Context, string, int) (TestConnection, error)
	Secure func(context.Context, string, int) (TestConnection, error)
}

// OpenWithTestDialers opens a normal leased Client while replacing only its
// network dial path. The constructor is intentionally narrow and internal:
// production callers should use Open.
func OpenWithTestDialers(statePath string, doer friends.Doer, dialers TestDialers) (*Client, error) {
	if dialers.TLS == nil || dialers.Secure == nil {
		return nil, ErrBootstrap
	}
	c, err := Open(statePath, doer)
	if err != nil {
		return nil, err
	}
	resume := c.checkpoint.Snapshot()
	c.dial = func(ctx context.Context, state authstate.State) (*Session, error) {
		return connectSessionWithResume(ctx, state, resume, testSessionDialers(dialers))
	}
	return c, nil
}

func testSessionDialers(dialers TestDialers) sessionDialers {
	wrap := func(d func(context.Context, string, int) (TestConnection, error)) wireDialer {
		return func(ctx context.Context, host string, port int) (*wireConn, error) {
			endpoint, err := d(ctx, host, port)
			if err != nil {
				return nil, err
			}
			if endpoint.Conn == nil {
				return nil, ErrBootstrap
			}
			return &wireConn{c: endpoint.Conn, secure: endpoint.Secure}, nil
		}
	}
	return sessionDialers{tls: wrap(dialers.TLS), secure: wrap(dialers.Secure)}
}
