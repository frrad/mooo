package connector

import (
	"context"
	"errors"
	"sync"
)

// worker is a cancellable background goroutine with a completion signal.
// Each start bumps id, so a run that has been superseded or stopped can tell
// it no longer owns the slot. The lifecycle mutex guards every field.
type worker struct {
	cancel context.CancelFunc
	done   chan struct{}
	id     uint64
}

// start runs fn in a new goroutine, replacing the slot's earlier run. The
// caller holds mu; the goroutine takes it again to clear the slot when fn
// returns, unless a later start or stop has already superseded it.
func (w *worker) start(mu *sync.Mutex, fn func(ctx context.Context, id uint64)) {
	ctx, cancel := context.WithCancel(context.Background())
	w.id++
	id := w.id
	done := make(chan struct{})
	w.cancel, w.done = cancel, done
	go func() {
		defer func() {
			close(done)
			mu.Lock()
			if w.id == id {
				w.cancel, w.done = nil, nil
			}
			mu.Unlock()
		}()
		fn(ctx, id)
	}()
}

// stop cancels the current run and supersedes it. The caller holds mu and
// joins the returned channel, nil when nothing was running, after releasing it.
func (w *worker) stop() <-chan struct{} {
	if w.cancel != nil {
		w.cancel()
	}
	done := w.done
	w.cancel, w.done = nil, nil
	w.id++
	return done
}

// running reports whether a run is in progress. The caller holds mu.
func (w *worker) running() bool { return w.cancel != nil }

// join waits for each non-nil channel to close, or for ctx to end.
func join(ctx context.Context, done ...<-chan struct{}) bool {
	for _, ch := range done {
		if ch == nil {
			continue
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return false
		}
	}
	return true
}

// ownerCleanup tracks the shutdown of the retained profile owner,
// KakaoClient.cleanup. The lifecycle mutex guards every field.
type ownerCleanup struct {
	// pumpDone remains associated with the owner while a Disconnect timeout
	// leaves its event pump running. It must not be discarded when the active
	// client fields are cleared: a later cleanup attempt must join the same
	// generation before releasing the profile owner.
	pumpDone chan struct{}
	// done closes when the in-flight Shutdown of the owner returns.
	done chan struct{}
	// busy marks a Shutdown of the owner in flight.
	busy bool
}

// begin marks a Shutdown of the owner in flight.
func (o *ownerCleanup) begin() {
	o.busy = true
	if o.done == nil {
		o.done = make(chan struct{})
	}
}

// retain makes c the profile owner that must be shut down before another
// session can open, with no shutdown in flight. The caller holds mu.
func (kc *KakaoClient) retain(c kakaoClient) {
	kc.cleanup = c
	kc.retained = ownerCleanup{}
}

// release ends a Shutdown attempt of c. If c is still the retained owner the
// attempt is no longer in flight, and a successful one releases the owner.
// A failed attempt keeps c retained so a later Disconnect or retry can finish
// it. The caller holds mu.
func (kc *KakaoClient) release(c kakaoClient, err error) {
	if kc.cleanup != c {
		return
	}
	kc.retained.busy = false
	if kc.retained.done != nil {
		close(kc.retained.done)
		kc.retained.done = nil
	}
	if err == nil {
		kc.cleanup = nil
		kc.retained.pumpDone = nil
	}
}

// openDetached opens a temporary profile client outside the connection
// lifecycle. It refuses while a session is connecting, connected, stopping or
// still retaining an owner, since the profile lease admits one owner.
func (kc *KakaoClient) openDetached() (kakaoClient, error) {
	kc.mu.Lock()
	busy := kc.stopping || kc.connecting || kc.cleanup != nil
	kc.mu.Unlock()
	if busy || kc.open == nil {
		return nil, errors.New("connector: profile cleanup or connection is still in progress")
	}
	return kc.open()
}

// releaseDetached shuts down a client from openDetached. When shutdown fails
// and no other owner is retained, c becomes the retained owner so a later
// Disconnect finishes its cleanup.
func (kc *KakaoClient) releaseDetached(c kakaoClient) error {
	ctx, cancel := context.WithTimeout(context.Background(), recoveryCleanupTimeout)
	defer cancel()
	err := c.Shutdown(ctx)
	if err != nil {
		kc.mu.Lock()
		if kc.cleanup == nil {
			kc.retain(c)
		}
		kc.mu.Unlock()
	}
	return err
}
