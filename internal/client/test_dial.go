package client

import (
	"context"
	"net"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/macweb"
)

// TestDialer returns an already-established synthetic LOCO transport for
// host. secure is nil for a plain TLS-framed endpoint. The scripted peer in
// internal/testsupport/loco provides one with Hosts.Dial.
type TestDialer func(ctx context.Context, host string, port int) (conn net.Conn, secure *loco.SecureV3, err error)

// TestDialers injects synthetic booking, check-in, and carriage transports.
// The connector integration tests use this seam to run the real bootstrap and
// session lifecycle against a scripted backend.
type TestDialers struct {
	TLS    TestDialer
	Secure TestDialer
}

// OpenWithTestDialers opens a normal leased Client while replacing only its
// network dial path. The constructor is intentionally narrow and internal:
// production callers should use OpenWithOptions. The optional options
// select the same production login policy while keeping synthetic dialers.
func OpenWithTestDialers(statePath string, doer macweb.Doer, dialers TestDialers, options ...OpenOptions) (*Client, error) {
	if dialers.TLS == nil || dialers.Secure == nil {
		return nil, errBootstrap
	}
	if len(options) > 1 {
		return nil, errBootstrap
	}
	var option OpenOptions
	if len(options) == 1 {
		option = options[0]
	}
	c, err := OpenWithOptions(statePath, doer, option)
	if err != nil {
		return nil, err
	}
	resume := option.loginResume(c.checkpoint.Snapshot())
	c.dial = func(ctx context.Context, state authstate.State) (*session, error) {
		return connectSession(ctx, state, c.sessionConfig(resume, testSessionDialers(dialers).observed(option.PacketObserver)))
	}
	return c, nil
}

func testSessionDialers(dialers TestDialers) sessionDialers {
	wrap := func(dial TestDialer) wireDialer {
		return func(ctx context.Context, host string, port int) (*wireConn, error) {
			conn, secure, err := dial(ctx, host, port)
			if err != nil {
				return nil, err
			}
			if conn == nil {
				return nil, errBootstrap
			}
			return &wireConn{c: conn, secure: secure}, nil
		}
	}
	return sessionDialers{tls: wrap(dialers.TLS), secure: wrap(dialers.Secure)}
}
