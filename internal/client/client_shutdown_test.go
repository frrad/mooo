package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/authstate"
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

func TestClientShutdownContextDoesNotWaitForConnect(t *testing.T) {
	dir := t.TempDir()
	leasePath := filepath.Join(dir, "profile.lock")
	lease, err := acquireProfileLease(leasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	lateConn := &stuckWriteConn{release: make(chan struct{}), entered: make(chan struct{})}
	lateSession := newSession(nil)
	lateSession.wire = &wireConn{c: lateConn}
	clock := &clientOutClock{}
	queue := &clientOutQueue{}
	owner, err := sessionlogin.NewOutSegmentTimeoutOwner(clock, queue, clientOutConfig{timeout: time.Second}, "agent", sessionlogin.OutSegmentTimeoutSelector, func() {})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lateSession.installOutSegmentSubmitter(lateSession.wire, owner, func(sessionlogin.OutSegmentWriteResult) {}); err != nil {
		t.Fatal(err)
	}
	if err := lateSession.writeRequest(context.Background(), lateSession.wire, 41, "PING", nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lateConn.Release)
	client := &Client{lease: lease}
	client.dial = func(context.Context, authstate.State) (*Session, error) {
		close(started)
		<-release
		return lateSession, nil
	}
	connectDone := make(chan error, 1)
	go func() { connectDone <- client.Connect(context.Background()) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("dial did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = client.Shutdown(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown while dial blocked error=%v, want deadline", err)
	}
	if _, err := acquireProfileLease(leasePath); !errors.Is(err, ErrProfileInUse) {
		t.Fatalf("lease after blocked shutdown error=%v, want %v", err, ErrProfileInUse)
	}
	// A blocked dial keeps ownership and makes the first shutdown incomplete.
	// Use a short context so this assertion catches accidental early cleanup.
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = client.Shutdown(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second shutdown while dial blocked error=%v, want deadline", err)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-connectDone:
		if !errors.Is(err, ErrClientClosed) {
			t.Fatalf("connect after shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("connect did not unwind after dial release")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = client.Shutdown(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown while late session worker blocked error=%v, want deadline", err)
	}
	if _, err := acquireProfileLease(leasePath); !errors.Is(err, ErrProfileInUse) {
		t.Fatalf("lease after late worker timeout error=%v, want %v", err, ErrProfileInUse)
	}
	lateConn.Release()
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	if err := client.Shutdown(ctx); err != nil {
		cancel()
		t.Fatalf("shutdown after dial release: %v", err)
	}
	cancel()
	otherLease, err := acquireProfileLease(leasePath)
	if err != nil {
		t.Fatalf("lease after confirmed connect shutdown: %v", err)
	}
	_ = otherLease.Close()
}

func TestClientShutdownContextDoesNotWaitForAnotherShutdown(t *testing.T) {
	conn := &stuckWriteConn{release: make(chan struct{}), entered: make(chan struct{})}
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
	if err := session.writeRequest(context.Background(), session.wire, 42, "PING", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-conn.entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not enter blocked write")
	}
	client := &Client{session: session}
	firstDone := make(chan error, 1)
	go func() { firstDone <- client.Shutdown(context.Background()) }()
	deadline := time.After(time.Second)
	for {
		client.mu.Lock()
		active := client.shutdownActive
		client.mu.Unlock()
		if active {
			break
		}
		select {
		case <-time.After(time.Millisecond):
		case <-deadline:
			t.Fatal("first shutdown did not start")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = client.Shutdown(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second shutdown error=%v, want deadline", err)
	}
	conn.Release()
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first shutdown did not finish")
	}
}

func TestClientCloseRetainsOwnershipThroughBlockedConnect(t *testing.T) {
	dir := t.TempDir()
	leasePath := filepath.Join(dir, "profile.lock")
	lease, err := acquireProfileLease(leasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	client := &Client{lease: lease, dial: func(context.Context, authstate.State) (*Session, error) {
		close(started)
		<-release
		return &Session{}, nil
	}}
	connectDone := make(chan error, 1)
	go func() { connectDone <- client.Connect(context.Background()) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("dial did not start")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("close while dial blocked: %v", err)
	}
	if _, err := acquireProfileLease(leasePath); !errors.Is(err, ErrProfileInUse) {
		t.Fatalf("lease after close during dial error=%v, want %v", err, ErrProfileInUse)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-connectDone:
		if !errors.Is(err, ErrClientClosed) {
			t.Fatalf("connect after close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("connect did not unwind after dial release")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	if err := client.Shutdown(ctx); err != nil {
		cancel()
		t.Fatalf("shutdown after close/connect completion: %v", err)
	}
	cancel()
	otherLease, err := acquireProfileLease(leasePath)
	if err != nil {
		t.Fatalf("lease after close cleanup: %v", err)
	}
	_ = otherLease.Close()
}
