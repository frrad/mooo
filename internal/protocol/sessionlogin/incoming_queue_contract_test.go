package sessionlogin

import (
	"reflect"
	"testing"
)

// incomingQueueContract is deliberately smaller than MKNest. It exposes the
// two source-observed admission boundaries while keeping execution timing
// injectable: HINT runs inline on the current queue or enqueues on another
// queue, while BLOCKSYNC uses a synchronous wait boundary.
type incomingQueueContract struct {
	events         []string
	queued         []func()
	panicOnEnqueue bool
	database       bool
	operationQueue bool
	sameQueue      bool
}

func (q *incomingQueueContract) enqueue(block func()) {
	q.events = append(q.events, "enqueue")
	if q.panicOnEnqueue {
		panic("queue admission exception")
	}
	q.queued = append(q.queued, block)
}

func (q *incomingQueueContract) wait(block func()) {
	q.events = append(q.events, "wait")
	block()
}

func (q *incomingQueueContract) runQueued() {
	for len(q.queued) != 0 {
		block := q.queued[0]
		q.queued = q.queued[1:]
		block()
	}
}

func performIncomingHintNest(q *incomingQueueContract, block func()) {
	if q.database && q.operationQueue {
		if q.sameQueue {
			block()
		} else {
			q.enqueue(block)
		}
	}
}

func dispatchIncomingHintWithNest(q *incomingQueueContract, delegate func()) {
	delegate()
	q.events = append(q.events, "receipt")
}

func dispatchIncomingBlockSync(q *incomingQueueContract, delegate func()) {
	delegate()
	q.events = append(q.events, "receipt")
}

func performIncomingBlockSyncNest(q *incomingQueueContract, block func()) {
	if !q.database || !q.operationQueue {
		return
	}
	if q.sameQueue {
		block()
		return
	}
	q.wait(block)
}

func TestIncomingHintReceiptFollowsEnqueueWithoutWaitingForWork(t *testing.T) {
	q := &incomingQueueContract{database: true, operationQueue: true}
	dispatchIncomingHintWithNest(q, func() {
		q.events = append(q.events, "delegate_begin")
		performIncomingHintNest(q, func() { q.events = append(q.events, "work") })
		q.events = append(q.events, "delegate_return")
	})
	if got, want := q.events, []string{"delegate_begin", "enqueue", "delegate_return", "receipt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("HINT admission events=%v want %v", got, want)
	}
	q.runQueued()
	if got, want := q.events, []string{"delegate_begin", "enqueue", "delegate_return", "receipt", "work"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("HINT deferred events=%v want %v", got, want)
	}
}

func TestIncomingHintSameQueueCompletesBeforeReceipt(t *testing.T) {
	q := &incomingQueueContract{database: true, operationQueue: true, sameQueue: true}
	dispatchIncomingHintWithNest(q, func() {
		q.events = append(q.events, "delegate_begin")
		performIncomingHintNest(q, func() { q.events = append(q.events, "work") })
		q.events = append(q.events, "delegate_return")
	})
	if got, want := q.events, []string{"delegate_begin", "work", "delegate_return", "receipt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("same-queue HINT events=%v want %v", got, want)
	}
}

func TestIncomingHintMissingNestContextSkipsWorkThenSendsReceipt(t *testing.T) {
	for _, tc := range []struct {
		name           string
		database       bool
		operationQueue bool
	}{
		{name: "nil database", operationQueue: true},
		{name: "nil queue", database: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &incomingQueueContract{database: tc.database, operationQueue: tc.operationQueue}
			dispatchIncomingHintWithNest(q, func() {
				q.events = append(q.events, "delegate_begin")
				performIncomingHintNest(q, func() { q.events = append(q.events, "work") })
				q.events = append(q.events, "delegate_return")
			})
			if got, want := q.events, []string{"delegate_begin", "delegate_return", "receipt"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("missing-context HINT events=%v want %v", got, want)
			}
		})
	}
}

func TestIncomingBlockSyncReceiptFollowsSynchronousWorkBoundary(t *testing.T) {
	q := &incomingQueueContract{database: true, operationQueue: true}
	dispatchIncomingBlockSync(q, func() {
		performIncomingBlockSyncNest(q, func() { q.events = append(q.events, "block-sync-state") })
	})
	if got, want := q.events, []string{"wait", "block-sync-state", "receipt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BLOCKSYNC events=%v want %v", got, want)
	}
}

func TestIncomingBlockSyncSameQueueRunsInlineBeforeReceipt(t *testing.T) {
	q := &incomingQueueContract{database: true, operationQueue: true, sameQueue: true}
	dispatchIncomingBlockSync(q, func() {
		performIncomingBlockSyncNest(q, func() { q.events = append(q.events, "block-sync-state") })
	})
	if got, want := q.events, []string{"block-sync-state", "receipt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("same-queue BLOCKSYNC events=%v want %v", got, want)
	}
}

func TestIncomingBlockSyncMissingNestContextSkipsWork(t *testing.T) {
	for _, tc := range []struct {
		name           string
		database       bool
		operationQueue bool
	}{
		{name: "nil database", operationQueue: true},
		{name: "nil queue", database: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &incomingQueueContract{database: tc.database, operationQueue: tc.operationQueue}
			dispatchIncomingBlockSync(q, func() {
				performIncomingBlockSyncNest(q, func() { q.events = append(q.events, "block-sync-state") })
			})
			if got, want := q.events, []string{"receipt"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("missing-context BLOCKSYNC events=%v want %v", got, want)
			}
		})
	}
}

func TestIncomingHintQueueExceptionUnwindsBeforeReceipt(t *testing.T) {
	q := &incomingQueueContract{database: true, operationQueue: true, panicOnEnqueue: true}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		dispatchIncomingHintWithNest(q, func() {
			q.events = append(q.events, "delegate_begin")
			performIncomingHintNest(q, func() {})
		})
	}()
	if recovered == nil {
		t.Fatal("queue exception did not unwind the callback")
	}
	if got, want := q.events, []string{"delegate_begin", "enqueue"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("exception events=%v want %v", got, want)
	}
}

func TestIncomingHintInlineWorkExceptionUnwindsBeforeReceipt(t *testing.T) {
	q := &incomingQueueContract{database: true, operationQueue: true, sameQueue: true}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		dispatchIncomingHintWithNest(q, func() {
			q.events = append(q.events, "delegate_begin")
			performIncomingHintNest(q, func() { panic("inline work exception") })
		})
	}()
	if recovered == nil {
		t.Fatal("inline exception did not unwind the callback")
	}
	if got, want := q.events, []string{"delegate_begin"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("inline exception events=%v want %v", got, want)
	}
}

func TestIncomingBlockSyncInlineWorkExceptionUnwindsBeforeReceipt(t *testing.T) {
	q := &incomingQueueContract{database: true, operationQueue: true, sameQueue: true}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		dispatchIncomingBlockSync(q, func() {
			performIncomingBlockSyncNest(q, func() { panic("write operation exception") })
		})
	}()
	if recovered == nil {
		t.Fatal("write exception did not unwind the callback")
	}
	if len(q.events) != 0 {
		t.Fatalf("exception events=%v want no events", q.events)
	}
}
