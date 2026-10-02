package sessionlogin

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

type outSubmitTestWorker struct {
	submits int
	closes  int
	done    func(OutSegmentWriteResult)
}

func (w *outSubmitTestWorker) Submit(_ context.Context, _ []byte, done func(OutSegmentWriteResult)) error {
	w.submits++
	w.done = done
	return nil
}

func (w *outSubmitTestWorker) Close() error {
	w.closes++
	return nil
}

func TestOutSegmentSubmitterOrdersEnableThenCompletionDisable(t *testing.T) {
	config := &outOwnerTestConfig{admission: time.Second, execution: 2 * time.Second}
	clock := &outOwnerTestClock{}
	queue := &outOwnerTestQueue{}
	worker := &outSubmitTestWorker{}
	var fired int
	owner := newTestOutOwner(t, config, clock, queue, &fired)
	var results []OutSegmentWriteResult
	submitter, err := NewOutSegmentSubmitter(owner, worker, func(result OutSegmentWriteResult) {
		results = append(results, result)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := submitter.Submit(context.Background(), []byte("frame")); err != nil {
		t.Fatal(err)
	}
	if worker.submits != 1 || len(queue.work) != 1 {
		t.Fatalf("submits=%d queued=%d", worker.submits, len(queue.work))
	}
	queue.runNext()
	if len(clock.timers) != 1 {
		t.Fatalf("timers=%d want 1", len(clock.timers))
	}
	worker.done(OutSegmentWriteResult{Complete: true})
	if len(results) != 1 || !results[0].Complete || len(queue.work) != 1 {
		t.Fatalf("results=%v queued=%d", results, len(queue.work))
	}
	queue.runNext()
	clock.timers[0].runEvenIfStopped()
	if !clock.timers[0].stopped || fired != 0 {
		t.Fatalf("timer stopped=%t fired=%d", clock.timers[0].stopped, fired)
	}
}

func TestOutSegmentSubmitterReplaysProgressBeforeTerminalCompletion(t *testing.T) {
	config := &outOwnerTestConfig{admission: time.Second, execution: time.Second}
	clock := &outOwnerTestClock{}
	queue := &outOwnerTestQueue{}
	worker := &outSubmitTestWorker{}
	owner := newTestOutOwner(t, config, clock, queue, new(int))
	var results []OutSegmentWriteResult
	submitter, err := NewOutSegmentSubmitter(owner, worker, func(result OutSegmentWriteResult) {
		results = append(results, result)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := submitter.Submit(context.Background(), []byte("frame")); err != nil {
		t.Fatal(err)
	}
	worker.done(OutSegmentWriteResult{Written: 2, Progress: true})
	worker.done(OutSegmentWriteResult{Written: 5, Complete: true})
	if len(results) != 2 || !results[0].Progress || results[0].Complete || !results[1].Complete {
		t.Fatalf("results=%v, want progress then terminal", results)
	}
	if len(queue.work) != 3 {
		t.Fatalf("queued enable/disables=%d want 3", len(queue.work))
	}
	queue.runNext()
	queue.runNext()
	queue.runNext()
}

func TestOutSegmentSubmitterTerminalWriteErrorDoesNotRetry(t *testing.T) {
	config := &outOwnerTestConfig{admission: time.Second, execution: time.Second}
	clock := &outOwnerTestClock{}
	queue := &outOwnerTestQueue{}
	worker := &outSubmitTestWorker{}
	owner := newTestOutOwner(t, config, clock, queue, new(int))
	var results []OutSegmentWriteResult
	submitter, err := NewOutSegmentSubmitter(owner, worker, func(result OutSegmentWriteResult) {
		results = append(results, result)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := submitter.Submit(context.Background(), []byte("frame")); err != nil {
		t.Fatal(err)
	}
	queue.runNext()
	wantErr := errors.New("ambiguous write")
	worker.done(OutSegmentWriteResult{Written: 1, Ambiguous: true, Err: wantErr})
	if worker.submits != 1 || worker.closes != 1 || len(results) != 1 || !errors.Is(results[0].Err, wantErr) {
		t.Fatalf("submits=%d closes=%d results=%v", worker.submits, worker.closes, results)
	}
	if err := submitter.Submit(context.Background(), []byte("retry")); !errors.Is(err, ErrOutSegmentSubmitterClosed) {
		t.Fatalf("retry error=%v", err)
	}
	for len(queue.work) > 0 {
		queue.runNext()
	}
	clock.timers[0].runEvenIfStopped()
}

type outSubmitPipeWorker struct {
	conn    net.Conn
	done    chan OutSegmentWriteResult
	submits int
	closes  int
}

func (w *outSubmitPipeWorker) Submit(_ context.Context, payload []byte, callback func(OutSegmentWriteResult)) error {
	w.submits++
	go func() {
		n, err := w.conn.Write(payload)
		callback(OutSegmentWriteResult{Written: n, Err: err})
		w.done <- OutSegmentWriteResult{Written: n, Err: err}
	}()
	return nil
}

func (w *outSubmitPipeWorker) Close() error {
	w.closes++
	return w.conn.Close()
}

func TestOutSegmentSubmitterCloseCancelsBlockedNetPipeWithoutRetry(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	worker := &outSubmitPipeWorker{conn: clientConn, done: make(chan OutSegmentWriteResult, 1)}
	config := &outOwnerTestConfig{admission: time.Second, execution: time.Second}
	clock := &outOwnerTestClock{}
	queue := &outOwnerTestQueue{}
	owner := newTestOutOwner(t, config, clock, queue, new(int))
	callbacks := 0
	submitter, err := NewOutSegmentSubmitter(owner, worker, func(OutSegmentWriteResult) { callbacks++ })
	if err != nil {
		t.Fatal(err)
	}
	if err := submitter.Submit(context.Background(), make([]byte, 32<<10)); err != nil {
		t.Fatal(err)
	}
	queue.runNext()
	submitter.Close()
	select {
	case result := <-worker.done:
		if result.Err == nil {
			t.Fatal("blocked write completed without error")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked write did not terminate after close")
	}
	if worker.submits != 1 || worker.closes != 1 || callbacks != 0 || !clock.timers[0].stopped {
		t.Fatalf("submits=%d closes=%d callbacks=%d stopped=%t", worker.submits, worker.closes, callbacks, clock.timers[0].stopped)
	}
}
