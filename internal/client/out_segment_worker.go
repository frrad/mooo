package client

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/frrad/mooo/internal/protocol/sessionlogin"
)

var ErrOutSegmentWorkerQueueFull = errors.New("client: out-segment write queue full")

const outSegmentWorkerQueueLimit = 64

// sessionOutSegmentWorker binds the reviewed asynchronous submitter seam to
// the existing Session write gate and write-failure cleanup. It is installed
// only by an explicit opt-in test/integration setup; default request writes
// remain on writeRawPayload's synchronous path.
type sessionOutSegmentWorker struct {
	session *Session
	wire    *wireConn
	mu      sync.Mutex
	closed  bool
	running bool
	done    chan struct{}
	queue   []sessionOutSegmentWork
}

type sessionOutSegmentWork struct {
	ctx     context.Context
	payload []byte
	done    func(sessionlogin.OutSegmentWriteResult)
}

func (w *sessionOutSegmentWorker) Submit(ctx context.Context, payload []byte, done func(sessionlogin.OutSegmentWriteResult)) error {
	if ctx == nil || done == nil {
		return fmt.Errorf("client: incomplete out-segment submission")
	}
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return ErrClosed
	}
	if len(w.queue) >= outSegmentWorkerQueueLimit {
		w.mu.Unlock()
		return ErrOutSegmentWorkerQueueFull
	}
	w.queue = append(w.queue, sessionOutSegmentWork{ctx: ctx, payload: append([]byte(nil), payload...), done: done})
	if !w.running {
		w.running = true
		w.done = make(chan struct{})
		go w.dispatch(w.done)
	}
	w.mu.Unlock()
	return nil
}

func (w *sessionOutSegmentWorker) dispatch(done chan struct{}) {
	defer func() {
		w.mu.Lock()
		if w.done == done {
			close(done)
			w.done = nil
		} else {
			close(done)
		}
		w.mu.Unlock()
	}()
	for {
		w.mu.Lock()
		if w.closed || len(w.queue) == 0 {
			w.queue = nil
			w.running = false
			w.mu.Unlock()
			return
		}
		work := w.queue[0]
		w.queue = w.queue[1:]
		w.mu.Unlock()
		reported := 0
		terminalSent := false
		written, err := w.session.writeRawPayloadProgress(work.ctx, w.wire, work.payload, func(n int, writeErr error) {
			reported += n
			if writeErr != nil {
				terminalSent = true
				work.done(sessionlogin.OutSegmentWriteResult{Written: reported, Complete: reported == len(work.payload), Ambiguous: reported > 0, Err: writeErr})
				return
			}
			work.done(sessionlogin.OutSegmentWriteResult{Written: n, Progress: true})
		})
		if !terminalSent {
			work.done(sessionlogin.OutSegmentWriteResult{Written: written, Complete: written == len(work.payload), Ambiguous: err != nil && written > 0 && written < len(work.payload), Err: err})
		}
	}
}

// Wait joins the current FIFO worker run after Close or after the queue has
// drained. It is intentionally separate from Close because a terminal
// callback may call Close from inside the worker itself.
func (w *sessionOutSegmentWorker) Wait(ctx context.Context) error {
	if ctx == nil {
		return context.Canceled
	}
	w.mu.Lock()
	done := w.done
	w.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *sessionOutSegmentWorker) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.queue = nil
	w.mu.Unlock()
	w.session.mu.Lock()
	if w.session.wire == w.wire {
		w.session.closing = true
	}
	w.session.mu.Unlock()
	return w.wire.close()
}

// installOutSegmentSubmitter binds an explicit worker to this Session and
// carriage. It intentionally has no default caller until producer/status
// readiness and configuration ownership are supplied by a reviewed layer.
func (s *Session) installOutSegmentSubmitter(wire *wireConn, owner *sessionlogin.OutSegmentTimeoutOwner, onResult func(sessionlogin.OutSegmentWriteResult)) (*sessionlogin.OutSegmentSubmitter, error) {
	if s == nil || wire == nil || owner == nil || onResult == nil {
		return nil, fmt.Errorf("client: incomplete out-segment integration")
	}
	worker := &sessionOutSegmentWorker{session: s, wire: wire}
	submitter, err := sessionlogin.NewOutSegmentSubmitter(owner, worker, onResult)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.closing || s.wire != wire || s.outSegmentSubmitter != nil {
		return nil, ErrClosed
	}
	s.outSegmentSubmitter = submitter
	return submitter, nil
}
