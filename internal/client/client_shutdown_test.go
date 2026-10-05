package client

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/sessionlogin"
)

type shutdownErrorConn struct {
	net.Conn
	err error
}

func (c *shutdownErrorConn) Close() error {
	_ = c.Conn.Close()
	return c.err
}

func cleanupShutdownSession(t *testing.T, session *Session, release func()) {
	t.Helper()
	t.Cleanup(func() {
		release()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := session.Shutdown(ctx); err != nil {
			t.Errorf("session cleanup: %v", err)
		}
	})
}

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
	cleanupShutdownSession(t, session, conn.Release)
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
	client.interruptTerminal()
	if _, err := acquireProfileLease(leasePath); !errors.Is(err, ErrProfileInUse) {
		t.Fatalf("lease after terminal interrupt = %v, want %v", err, ErrProfileInUse)
	}

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
	t.Cleanup(func() { _ = lease.Close() })
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	lateConn := &stuckWriteConn{release: make(chan struct{}), entered: make(chan struct{})}
	lateSession := newSession(nil)
	lateSession.wire = &wireConn{c: lateConn}
	cleanupShutdownSession(t, lateSession, lateConn.Release)
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
	connectFinished := make(chan struct{})
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		lateConn.Release()
		select {
		case <-connectFinished:
		case <-time.After(time.Second):
			t.Error("connect did not finish during cleanup")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := client.Shutdown(ctx); err != nil {
			t.Errorf("client cleanup: %v", err)
		}
	})
	go func() {
		defer close(connectFinished)
		connectDone <- client.Connect(context.Background())
	}()
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
	cleanupShutdownSession(t, session, conn.Release)
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
	firstFinished := make(chan struct{})
	t.Cleanup(func() {
		conn.Release()
		select {
		case <-firstFinished:
		case <-time.After(time.Second):
			t.Errorf("first shutdown did not finish during cleanup")
		}
	})
	go func() {
		defer close(firstFinished)
		firstDone <- client.Shutdown(context.Background())
	}()
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
	connectFinished := make(chan struct{})
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		select {
		case <-connectFinished:
		case <-time.After(time.Second):
			t.Error("connect did not finish during cleanup")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := client.Shutdown(ctx); err != nil {
			t.Errorf("client cleanup: %v", err)
		}
	})
	go func() {
		defer close(connectFinished)
		connectDone <- client.Connect(context.Background())
	}()
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

func TestClientShutdownPreservesSessionInterruptError(t *testing.T) {
	left, right := net.Pipe()
	defer func() { _ = right.Close() }()
	want := errors.New("transport close sentinel")
	session := newSession(nil)
	session.wire = &wireConn{c: &shutdownErrorConn{Conn: left, err: want}}
	client := &Client{session: session}
	err := client.Shutdown(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("Shutdown error=%v, want %v", err, want)
	}
}

func TestClientShutdownJoinsNormalAndCleanupSessions(t *testing.T) {
	leasePath := filepath.Join(t.TempDir(), "profile.lock")
	lease, err := acquireProfileLease(leasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	cleanupConn := &stuckWriteConn{release: make(chan struct{}), entered: make(chan struct{})}
	cleanup := newSession(nil)
	cleanup.wire = &wireConn{c: cleanupConn}
	cleanupShutdownSession(t, cleanup, cleanupConn.Release)
	clock, queue := &clientOutClock{}, &clientOutQueue{}
	owner, err := sessionlogin.NewOutSegmentTimeoutOwner(clock, queue, clientOutConfig{timeout: time.Second}, "agent", sessionlogin.OutSegmentTimeoutSelector, func() {})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cleanup.installOutSegmentSubmitter(cleanup.wire, owner, func(sessionlogin.OutSegmentWriteResult) {}); err != nil {
		t.Fatal(err)
	}
	// The primary is already joinable. Only the retained cleanup worker can
	// force a deadline, so this catches accidentally joining just the primary.
	client := &Client{session: newSession(nil), cleanupSession: cleanup, lease: lease}
	t.Cleanup(func() {
		cleanupConn.Release()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := client.Shutdown(ctx); err != nil {
			t.Errorf("client cleanup: %v", err)
		}
	})
	if err := cleanup.writeRequest(context.Background(), cleanup.wire, 42, "PING", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cleanupConn.entered:
	case <-time.After(time.Second):
		t.Fatal("retained worker did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = client.Shutdown(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error=%v, want deadline", err)
	}
	otherLease, err := acquireProfileLease(leasePath)
	if otherLease != nil {
		_ = otherLease.Close()
	}
	if !errors.Is(err, ErrProfileInUse) {
		t.Fatalf("lease before retained worker joins=%v, want %v", err, ErrProfileInUse)
	}
	cleanupConn.Release()
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	if err := client.Shutdown(ctx); err != nil {
		cancel()
		t.Fatalf("joined Shutdown: %v", err)
	}
	cancel()
	otherLease, err = acquireProfileLease(leasePath)
	if err != nil {
		t.Fatalf("lease after both sessions joined: %v", err)
	}
	_ = otherLease.Close()
}

func TestClientShutdownRetainsOwnershipForAdmittedCommit(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	leasePath := filepath.Join(dir, "profile.lock")
	lease, err := acquireProfileLease(leasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	checkpoint, err := continuity.Open(filepath.Join(dir, "continuity"))
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{lease: lease, checkpoint: checkpoint, pendingCommits: map[int64][]int64{42: {100}}}
	client.commitMu.Lock()
	commitResult := make(chan error, 1)
	commitFinished := make(chan struct{})
	var releaseCommit sync.Once
	release := func() { releaseCommit.Do(func() { client.commitMu.Unlock() }) }
	t.Cleanup(func() {
		release()
		select {
		case <-commitFinished:
		case <-time.After(time.Second):
			t.Errorf("CommitEvent did not finish during cleanup")
		}
	})
	go func() {
		commitResult <- client.CommitEvent(events.TextMessage{ChatID: 42, LogID: 100})
		close(commitFinished)
	}()
	deadline := time.After(time.Second)
	for {
		client.mu.Lock()
		active := client.commitActive
		client.mu.Unlock()
		if active == 1 {
			break
		}
		select {
		case <-time.After(time.Millisecond):
		case <-deadline:
			release()
			t.Fatal("CommitEvent did not enter persistence gate")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err = client.Shutdown(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown during admitted commit error=%v, want deadline", err)
	}
	if _, err := acquireProfileLease(leasePath); !errors.Is(err, ErrProfileInUse) {
		t.Fatalf("lease during admitted commit error=%v, want %v", err, ErrProfileInUse)
	}
	release()
	select {
	case err := <-commitResult:
		if err != nil {
			t.Fatalf("CommitEvent: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CommitEvent did not complete")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	if err := client.Shutdown(ctx); err != nil {
		cancel()
		t.Fatalf("shutdown after commit: %v", err)
	}
	cancel()
	otherLease, err := acquireProfileLease(leasePath)
	if err != nil {
		t.Fatalf("lease after commit: %v", err)
	}
	_ = otherLease.Close()
}
