package connector

import (
	"context"
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
