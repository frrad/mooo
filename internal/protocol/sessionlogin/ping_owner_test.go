package sessionlogin

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPingIntentExecutorKeepsInlineAndQueuedOrder(t *testing.T) {
	executor := newPingIntentExecutor()
	if err := executor.Apply([]PingIntent{
		{Kind: PingIntentQueueCancel},
		{Kind: PingIntentOrdinaryRequest},
		{Kind: PingIntentQueueSchedule},
		{Kind: PingIntentForwardCompletion, Response: "packet"},
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := executor.eventLog(), []string{
		"queue_cancel", "ordinary_request", "queue_schedule", "forward_completion",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events=%v want=%v", got, want)
	}
	executor.runAll()
	if !executor.runNextTimer() {
		t.Fatal("scheduled timer did not fire")
	}
	if got, want := executor.eventLog(), []string{
		"queue_cancel", "ordinary_request", "queue_schedule", "forward_completion",
		"queue_cancel_apply", "queue_schedule_apply", "timer_fire",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events after timer=%v want=%v", got, want)
	}
}

func TestPingIntentExecutorCancellationInvalidatesOnlyTimerWork(t *testing.T) {
	executor := newPingIntentExecutor()
	if err := executor.Apply([]PingIntent{{Kind: PingIntentQueueSchedule}}); err != nil {
		t.Fatal(err)
	}
	executor.runAll()
	if err := executor.Apply([]PingIntent{{Kind: PingIntentQueueCancel}}); err != nil {
		t.Fatal(err)
	}
	if !executor.fireTimer(1) {
		t.Fatal("eligible timer did not fire before queued cancellation drained")
	}
	after := newPingIntentExecutor()
	if err := after.Apply([]PingIntent{{Kind: PingIntentQueueSchedule}}); err != nil {
		t.Fatal(err)
	}
	after.runAll()
	if err := after.Apply([]PingIntent{{Kind: PingIntentQueueCancel}}); err != nil {
		t.Fatal(err)
	}
	after.runAll()
	if after.fireTimer(1) {
		t.Fatal("cancelled timer fired after cancellation drained")
	}
	if got, want := executor.eventLog(), []string{"queue_schedule", "queue_schedule_apply", "queue_cancel", "timer_fire"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events=%v want=%v", got, want)
	}
}

func TestPingIntentExecutorRejectsStaleTimerToken(t *testing.T) {
	executor := newPingIntentExecutor()
	if err := executor.Apply([]PingIntent{{Kind: PingIntentQueueSchedule}}); err != nil {
		t.Fatal(err)
	}
	executor.runAll()
	if err := executor.Apply([]PingIntent{{Kind: PingIntentQueueSchedule}}); err != nil {
		t.Fatal(err)
	}
	executor.runAll()
	if executor.fireTimer(1) {
		t.Fatal("stale timer token fired")
	}
	if !executor.fireTimer(2) {
		t.Fatal("current timer token did not fire")
	}
}

func TestPingIntentExecutorcloseCancelsTimersAndFailsPendingOnce(t *testing.T) {
	executor := newPingIntentExecutor()
	if err := executor.Apply([]PingIntent{{Kind: PingIntentQueueSchedule}}); err != nil {
		t.Fatal(err)
	}
	var got []error
	executor.addPending(func(err error) { got = append(got, err) })
	wantErr := errors.New("synthetic disconnect")
	executor.close(wantErr)
	executor.close(errors.New("duplicate disconnect"))
	if executor.runNextTimer() {
		t.Fatal("timer fired after close")
	}
	if len(got) != 1 || !errors.Is(got[0], wantErr) {
		t.Fatalf("pending errors=%v want one terminal error %v", got, wantErr)
	}
}

func TestPingIntentExecutorCompletedPendingIsNotclosedAgain(t *testing.T) {
	executor := newPingIntentExecutor()
	var got []error
	id := executor.addPending(func(err error) { got = append(got, err) })
	wantErr := errors.New("synthetic response error")
	if !executor.completePending(id, wantErr) || executor.completePending(id, errors.New("duplicate")) {
		t.Fatal("pending completion did not enforce one-shot removal")
	}
	executor.close(errors.New("synthetic disconnect"))
	if len(got) != 1 || !errors.Is(got[0], wantErr) {
		t.Fatalf("pending errors=%v want one response error %v", got, wantErr)
	}
}

func TestPingIntentExecutorConcurrentCompletionAndCloseIsOneShot(t *testing.T) {
	executor := newPingIntentExecutor()
	var calls atomic.Int32
	id := executor.addPending(func(error) { calls.Add(1) })
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		<-start
		executor.completePending(id, errors.New("response"))
	}()
	go func() {
		defer group.Done()
		<-start
		executor.close(errors.New("disconnect"))
	}()
	close(start)
	group.Wait()
	if calls.Load() != 1 {
		t.Fatalf("pending callback calls=%d want one", calls.Load())
	}
}

func TestPingIntentExecutorPreservesCompletionSelectedBeforeclose(t *testing.T) {
	executor := newPingIntentExecutor()
	if err := executor.Apply([]PingIntent{
		{Kind: PingIntentQueueCancel},
		{Kind: PingIntentForwardCompletion, Response: "packet"},
	}); err != nil {
		t.Fatal(err)
	}
	executor.runAll()
	executor.close(errors.New("synthetic disconnect"))
	if got, want := executor.forwardedIntents(), []PingIntent{{Kind: PingIntentForwardCompletion, Response: "packet"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("forwarded=%#v want=%#v", got, want)
	}
}

func TestPingIntentExecutorConsumesApprovedLifecycleVectors(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q4-q5-lifecycle.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var vectors pingIntentVectors
	if err := dec.Decode(&vectors); err != nil {
		t.Fatal(err)
	}
	for _, tc := range vectors.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			executor := newPingIntentExecutor()
			var intents []PingIntent
			switch tc.Kind {
			case "request-entered":
				intents = PlanPingRequestEntered(tc.Accepted, tc.CallbackPresent, nil, nil)
			case "completion":
				intents = PlanPingCompletion(tc.CallbackPresent, nil, nil)
			case "without-completion":
				intents = PlanPingWithoutCompletion()
			default:
				t.Fatalf("unsupported lifecycle kind %q", tc.Kind)
			}
			if err := executor.Apply(intents); err != nil {
				t.Fatal(err)
			}
			if got := executor.eventLog(); !reflect.DeepEqual(got, tc.Want) {
				t.Fatalf("executor events=%v want=%v", got, tc.Want)
			}
		})
	}
}
