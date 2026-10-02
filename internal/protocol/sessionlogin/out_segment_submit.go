package sessionlogin

import (
	"context"
	"errors"
	"sync"
)

var (
	ErrOutSegmentSubmitterClosed = errors.New("sessionlogin: out-segment submitter closed")
)

// OutSegmentWriteResult is the one worker result delivered to the submitter.
// Ambiguous marks a partial transport write whose framing cannot be safely
// reused. Context cancellation before any bytes, and cancellation after a
// complete frame, remain non-terminal per the Session write policy. Complete
// identifies the source callback shape but does not alter timeout disarming
// order.
type OutSegmentWriteResult struct {
	Written   int
	Complete  bool
	Progress  bool
	Ambiguous bool
	Err       error
}

// OutSegmentWriteWorker returns after submitting an asynchronous write. It
// may invoke done for positive incomplete progress and once for the terminal
// outcome; callbacks must not be synchronous from Submit. Close must interrupt
// an in-flight write when the worker supports cancellation.
type OutSegmentWriteWorker interface {
	Submit(context.Context, []byte, func(OutSegmentWriteResult)) error
	Close() error
}

type outSegmentWriteWaiter interface {
	Wait(context.Context) error
}

type outSegmentSubmission struct {
	armed       bool
	dispatching bool
	terminal    bool
	events      []OutSegmentWriteResult
	onResult    func(OutSegmentWriteResult)
}

// OutSegmentSubmitter binds the reviewed submission/callback ordering to an
// injected worker and out-segment owner. It is opt-in; it does not select
// status/configuration readiness or alter Session construction.
type OutSegmentSubmitter struct {
	mu       sync.Mutex
	owner    *OutSegmentTimeoutOwner
	worker   OutSegmentWriteWorker
	onResult func(OutSegmentWriteResult)
	nextID   uint64
	pending  map[uint64]*outSegmentSubmission
	closed   bool
}

func NewOutSegmentSubmitter(owner *OutSegmentTimeoutOwner, worker OutSegmentWriteWorker, onResult func(OutSegmentWriteResult)) (*OutSegmentSubmitter, error) {
	if owner == nil || worker == nil || onResult == nil {
		return nil, errors.New("sessionlogin: incomplete out-segment submitter")
	}
	return &OutSegmentSubmitter{owner: owner, worker: worker, onResult: onResult, pending: make(map[uint64]*outSegmentSubmission)}, nil
}

// Submit starts one asynchronous worker submission. The timeout enable is
// queued only after worker submission returns. A completion that races this
// boundary is held until the enable has been admitted, preserving the source
// ordering before the completion-side disable.
func (s *OutSegmentSubmitter) Submit(ctx context.Context, payload []byte) error {
	return s.SubmitWithResult(ctx, payload, nil)
}

// SubmitWithResult is the correlated form used by a Session request. The
// callback belongs only to this submission; the constructor callback remains a
// default observer for standalone callers.
func (s *OutSegmentSubmitter) SubmitWithResult(ctx context.Context, payload []byte, onResult func(OutSegmentWriteResult)) error {
	if s == nil || ctx == nil {
		return ErrOutSegmentSubmitterClosed
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrOutSegmentSubmitterClosed
	}
	s.nextID++
	id := s.nextID
	s.pending[id] = &outSegmentSubmission{onResult: onResult}
	s.mu.Unlock()

	if err := s.worker.Submit(ctx, payload, func(result OutSegmentWriteResult) { s.complete(id, result) }); err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return err
	}
	_, err := s.owner.Toggle(1)
	if err != nil {
		s.terminateSubmission(id)
		return err
	}
	// A non-positive configured timeout intentionally skips timer admission;
	// the submitted write and its callback still proceed.

	var events []OutSegmentWriteResult
	s.mu.Lock()
	state, ok := s.pending[id]
	if ok {
		state.armed = true
		state.dispatching = true
		events = append(events, state.events...)
		state.events = nil
	}
	s.mu.Unlock()
	for {
		for i := range events {
			s.finish(&events[i], state.onResult)
		}
		s.mu.Lock()
		state, ok = s.pending[id]
		if !ok {
			s.mu.Unlock()
			break
		}
		events = append(events[:0], state.events...)
		state.events = nil
		if len(events) == 0 {
			state.dispatching = false
			if state.terminal {
				delete(s.pending, id)
			}
			s.mu.Unlock()
			break
		}
		s.mu.Unlock()
	}
	return nil
}

func (s *OutSegmentSubmitter) complete(id uint64, result OutSegmentWriteResult) {
	s.mu.Lock()
	state, ok := s.pending[id]
	if !ok || s.closed {
		s.mu.Unlock()
		return
	}
	if state.terminal {
		s.mu.Unlock()
		return
	}
	if !state.armed || state.dispatching {
		state.events = append(state.events, result)
		if !result.Progress {
			state.terminal = true
		}
		s.mu.Unlock()
		return
	}
	if !result.Progress {
		state.terminal = true
		delete(s.pending, id)
	}
	s.mu.Unlock()
	s.finish(&result, state.onResult)
}

func (s *OutSegmentSubmitter) finish(result *OutSegmentWriteResult, onResult func(OutSegmentWriteResult)) {
	// Disable is queued before result forwarding. Ambiguous partial writes
	// additionally close the owner/worker, preventing retries and future timer
	// delivery; ordinary context errors keep the carriage reusable.
	_, _ = s.owner.Disable()
	if onResult == nil {
		onResult = s.onResult
	}
	onResult(*result)
	if result.Ambiguous {
		s.terminate()
	}
}

func (s *OutSegmentSubmitter) terminateSubmission(id uint64) {
	s.mu.Lock()
	delete(s.pending, id)
	s.mu.Unlock()
	s.terminate()
}

func (s *OutSegmentSubmitter) terminate() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	worker := s.worker
	clear(s.pending)
	s.mu.Unlock()
	s.owner.Close()
	_ = worker.Close()
}

// Close invalidates future submissions and stale worker callbacks, then
// interrupts the worker. A callback already selected by complete may still
// finish outside the lock; this method does not join arbitrary worker code.
func (s *OutSegmentSubmitter) Close() {
	if s == nil {
		return
	}
	s.terminate()
}

// Wait joins a worker run when the injected worker exposes an explicit join
// operation. Close intentionally does not call it because a terminal callback
// may close the submitter from inside the worker stack.
func (s *OutSegmentSubmitter) Wait(ctx context.Context) error {
	if s == nil || ctx == nil {
		return ErrOutSegmentSubmitterClosed
	}
	s.mu.Lock()
	worker := s.worker
	s.mu.Unlock()
	if waiter, ok := worker.(outSegmentWriteWaiter); ok {
		return waiter.Wait(ctx)
	}
	return nil
}
