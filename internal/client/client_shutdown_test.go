package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/sessionlogin"
)

func TestClientShutdownRetainsOwnershipUntilSessionWorkerJoins(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	leasePath := filepath.Join(dir, "profile.lock")
	lease, err := acquireProfileLease(leasePath)
	if err != nil {
		t.Fatal(err)
	}
	conn := &stuckWriteConn{release: make(chan struct{}), entered: make(chan struct{})}
	var client *Client
	t.Cleanup(func() {
		conn.Release()
		if client != nil {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			if err := client.Shutdown(ctx); err != nil {
				t.Errorf("shutdown cleanup: %v", err)
			}
			cancel()
		}
		_ = lease.Close()
	})

	session := newSession(nil)
	session.wire = &wireConn{c: conn}
	clock := &clientOutClock{}
	queue := &clientOutQueue{}
	owner, err := sessionlogin.NewOutSegmentTimeoutOwner(clock, queue, clientOutConfig{timeout: time.Second}, "agent", sessionlogin.OutSegmentTimeoutSelector, func() {})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.installOutSegmentSubmitter(session.wire, owner, func(sessionlogin.OutSegmentWriteResult) {}); err != nil {
		t.Fatal(err)
	}
	if err := session.writeRequest(context.Background(), session.wire, 41, "PING", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-conn.entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not enter blocked write")
	}
	checkpoint, err := continuity.Open(filepath.Join(dir, "continuity"))
	if err != nil {
		t.Fatal(err)
	}
	client = &Client{session: session, checkpoint: checkpoint, lease: lease}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = client.Shutdown(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first shutdown error=%v, want bounded worker deadline", err)
	}
	if _, err := acquireProfileLease(leasePath); !errors.Is(err, ErrProfileInUse) {
		t.Fatalf("lease after timeout error=%v, want %v", err, ErrProfileInUse)
	}
	if err := client.Connect(context.Background()); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("Connect after shutdown admission error=%v, want %v", err, ErrClientClosed)
	}

	conn.Release()
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	err = client.Shutdown(ctx)
	cancel()
	if err != nil {
		t.Fatalf("retry shutdown: %v", err)
	}
	otherLease, err := acquireProfileLease(leasePath)
	if err != nil {
		t.Fatalf("lease after confirmed shutdown: %v", err)
	}
	_ = otherLease.Close()
}
