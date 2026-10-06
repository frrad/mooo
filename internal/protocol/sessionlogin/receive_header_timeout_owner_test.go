package sessionlogin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type ownerTestTimer struct {
	fn      func()
	stopped bool
}

func (t *ownerTestTimer) Stop() bool {
	wasActive := !t.stopped
	t.stopped = true
	return wasActive
}

func (t *ownerTestTimer) runEvenIfStopped() { t.fn() }

type ownerTestClock struct {
	timers []*ownerTestTimer
	delays []time.Duration
}

func (c *ownerTestClock) AfterFunc(delay time.Duration, fn func()) ReceiveHeaderTimeoutTimer {
	t := &ownerTestTimer{fn: fn}
	c.timers = append(c.timers, t)
	c.delays = append(c.delays, delay)
	return t
}

type ownerTestQueue struct{ work []func() }

func (q *ownerTestQueue) Enqueue(fn func()) { q.work = append(q.work, fn) }
func (q *ownerTestQueue) runNext() {
	fn := q.work[0]
	q.work = q.work[1:]
	fn()
}

type ownerTestConfig struct {
	admission, execution time.Duration
	reads                int
}

func mutateOwnerTestByte(value *byte) { *value = 0 }

func (c *ownerTestConfig) ReceiveHeaderTimeout() time.Duration {
	c.reads++
	if c.reads == 1 {
		return c.admission
	}
	return c.execution
}

func newTestOwner(t *testing.T, config *ownerTestConfig, clock *ownerTestClock, queue *ownerTestQueue, fired *[]int64) *ReceiveHeaderTimeoutOwner {
	t.Helper()
	owner, err := NewReceiveHeaderTimeoutOwner(clock, queue, config, "agent", receiveHeaderTimeoutSelector, func(tag int64) {
		*fired = append(*fired, tag)
	})
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestNewReceiveHeaderTimeoutOwnerRejectsUnsupportedSelector(t *testing.T) {
	_, err := NewReceiveHeaderTimeoutOwner(&ownerTestClock{}, &ownerTestQueue{}, &ownerTestConfig{admission: time.Second}, "agent", "otherSelector:", func(int64) {})
	if err == nil {
		t.Fatal("unsupported selector accepted")
	}
}

func TestReceiveHeaderTimeoutOwnerReadsAdmissionAndExecutionAndAllowsZeroDelay(t *testing.T) {
	config := &ownerTestConfig{admission: 20 * time.Second, execution: 0}
	clock := &ownerTestClock{}
	queue := &ownerTestQueue{}
	var fired []int64
	owner := newTestOwner(t, config, clock, queue, &fired)
	if admitted, err := owner.Toggle(1, 7); err != nil || !admitted {
		t.Fatalf("arm=%t err=%v", admitted, err)
	}
	if config.reads != 1 || len(queue.work) != 1 {
		t.Fatalf("admission reads=%d queued=%d", config.reads, len(queue.work))
	}
	queue.runNext()
	if config.reads != 2 || !reflect.DeepEqual(clock.delays, []time.Duration{0}) {
		t.Fatalf("execution reads=%d delays=%v", config.reads, clock.delays)
	}
	clock.timers[0].runEvenIfStopped()
	if !reflect.DeepEqual(fired, []int64{7}) {
		t.Fatalf("fired=%v", fired)
	}
}

func TestReceiveHeaderTimeoutOwnerCapturesEnableByteBeforeQueueDispatch(t *testing.T) {
	config := &ownerTestConfig{admission: time.Second, execution: time.Second}
	clock := &ownerTestClock{}
	queue := &ownerTestQueue{}
	var fired []int64
	owner := newTestOwner(t, config, clock, queue, &fired)
	enable := byte(1)
	if _, err := owner.Toggle(enable, 7); err != nil {
		t.Fatal(err)
	}
	mutateOwnerTestByte(&enable)
	queue.runNext()
	if len(clock.timers) != 1 {
		t.Fatalf("captured enable scheduled timers=%d", len(clock.timers))
	}
}

func TestReceiveHeaderTimeoutOwnerLaterEnableAfterDisableSchedulesAgain(t *testing.T) {
	config := &ownerTestConfig{admission: time.Second, execution: time.Second}
	clock := &ownerTestClock{}
	queue := &ownerTestQueue{}
	var fired []int64
	owner := newTestOwner(t, config, clock, queue, &fired)
	for _, enable := range []byte{1, 0, 1} {
		if _, err := owner.Toggle(enable, 7); err != nil {
			t.Fatal(err)
		}
	}
	for len(queue.work) > 0 {
		queue.runNext()
	}
	if len(clock.timers) != 2 || !clock.timers[0].stopped || clock.timers[1].stopped {
		t.Fatalf("timers=%d stopped=[%t %t], want canceled first and active later", len(clock.timers), clock.timers[0].stopped, clock.timers[1].stopped)
	}
}

func TestReceiveHeaderTimeoutOwnerPreservesNegativeExecutionDelay(t *testing.T) {
	config := &ownerTestConfig{admission: time.Second, execution: -time.Second}
	clock := &ownerTestClock{}
	queue := &ownerTestQueue{}
	var fired []int64
	owner := newTestOwner(t, config, clock, queue, &fired)
	if _, err := owner.Toggle(1, 7); err != nil {
		t.Fatal(err)
	}
	queue.runNext()
	if config.reads != 2 || !reflect.DeepEqual(clock.delays, []time.Duration{-time.Second}) {
		t.Fatalf("execution reads=%d delays=%v", config.reads, clock.delays)
	}
}

func TestReceiveHeaderTimeoutOwnerRepeatedEnableKeepsEachTimerAndExactCancelCancelsAllMatching(t *testing.T) {
	config := &ownerTestConfig{admission: time.Second, execution: 2 * time.Second}
	clock := &ownerTestClock{}
	queue := &ownerTestQueue{}
	var fired []int64
	owner := newTestOwner(t, config, clock, queue, &fired)
	if _, err := owner.Toggle(1, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Toggle(1, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Toggle(1, 8); err != nil {
		t.Fatal(err)
	}
	for len(queue.work) > 0 {
		queue.runNext()
	}
	if len(clock.timers) != 3 {
		t.Fatalf("scheduled timers=%d want 3", len(clock.timers))
	}
	if admitted, err := owner.Toggle(0, 7); err != nil || !admitted {
		t.Fatalf("exact cancellation rejected admitted=%t err=%v", admitted, err)
	}
	queue.runNext()
	for _, timer := range clock.timers {
		timer.runEvenIfStopped()
	}
	if !reflect.DeepEqual(fired, []int64{8}) {
		t.Fatalf("fired=%v want [8]", fired)
	}
}

func TestReceiveHeaderTimeoutOwnerGatesDisableAtAdmission(t *testing.T) {
	config := &ownerTestConfig{admission: 0, execution: time.Second}
	clock := &ownerTestClock{}
	queue := &ownerTestQueue{}
	var fired []int64
	owner := newTestOwner(t, config, clock, queue, &fired)
	if admitted, err := owner.Toggle(0, 7); err != nil || admitted {
		t.Fatalf("zero-timeout disable admitted=%t err=%v", admitted, err)
	}
	if len(queue.work) != 0 {
		t.Fatalf("zero-timeout disable queued=%d", len(queue.work))
	}

	config.admission = time.Second
	if admitted, err := owner.Toggle(0, -1); err != nil || admitted {
		t.Fatalf("negative-tag disable admitted=%t err=%v", admitted, err)
	}
	if len(queue.work) != 0 {
		t.Fatalf("negative-tag disable queued=%d", len(queue.work))
	}
}

func TestReceiveHeaderTimeoutOwnerUsesCanonicalTimeoutVectors(t *testing.T) {
	vectors, err := loadReceiveHeaderTimeoutContract(filepath.Join("..", "..", "..", "research", "fixtures", "reconnect", "rc-q5-timeout-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range vectors.Cases {
		if tc.Kind != "timeout-admission" && tc.Kind != "timeout-enable" && tc.Kind != "timeout-disable" {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			admission, err := timeoutVectorDuration(tc.TimeoutSeconds)
			if err != nil {
				t.Fatal(err)
			}
			execution, err := timeoutVectorDuration(tc.ExecutionTimeoutSeconds)
			if err != nil {
				t.Fatal(err)
			}
			enableByte := byte(0)
			if tc.EnableByte != nil {
				enableByte = byte(*tc.EnableByte)
			} else if tc.Enable != nil && *tc.Enable {
				enableByte = 1
			}
			input := ReceiveHeaderTimeoutInput{AdmissionTimeout: admission, ExecutionTimeout: execution, EnableByte: enableByte, Owner: "agent", RequestTag: *tc.Tag}
			planned, err := PlanReceiveHeaderTimeout(input)
			if err != nil {
				t.Fatal(err)
			}
			expectsAdmission := len(planned) > 0 && planned[0].Kind != "no_enqueue"

			config := &ownerTestConfig{admission: admission, execution: execution}
			clock := &ownerTestClock{}
			queue := &ownerTestQueue{}
			var fired []int64
			owner := newTestOwner(t, config, clock, queue, &fired)
			admitted, err := owner.Toggle(enableByte, *tc.Tag)
			if err != nil {
				t.Fatal(err)
			}
			if admitted != expectsAdmission {
				t.Fatalf("admitted=%t planner=%t effects=%#v", admitted, expectsAdmission, planned)
			}
			if admitted {
				queue.runNext()
			}
			if enableByte == 1 && expectsAdmission && len(clock.timers) != 1 {
				t.Fatalf("enable timers=%d effects=%#v", len(clock.timers), planned)
			}
			if enableByte == 1 && expectsAdmission {
				if !reflect.DeepEqual(clock.delays, []time.Duration{execution}) {
					t.Fatalf("execution delays=%v want [%s] effects=%#v", clock.delays, execution, planned)
				}
				clock.timers[0].runEvenIfStopped()
				if !reflect.DeepEqual(fired, []int64{*tc.Tag}) {
					t.Fatalf("fired=%v want [%d] effects=%#v", fired, *tc.Tag, planned)
				}
			}
		})
	}
}

func TestReceiveHeaderTimeoutOwnerCanonicalDisableCancelsOnlyMatchingTag(t *testing.T) {
	vectors, err := loadReceiveHeaderTimeoutContract(filepath.Join("..", "..", "..", "research", "fixtures", "reconnect", "rc-q5-timeout-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range vectors.Cases {
		if tc.Kind != "timeout-disable" || tc.TimeoutSeconds == nil || tc.Tag == nil {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			admission, err := timeoutVectorDuration(tc.TimeoutSeconds)
			if err != nil {
				t.Fatal(err)
			}
			execution, err := timeoutVectorDuration(tc.ExecutionTimeoutSeconds)
			if err != nil {
				t.Fatal(err)
			}
			enableByte := byte(0)
			if tc.EnableByte != nil {
				enableByte = byte(*tc.EnableByte)
			}
			config := &ownerTestConfig{admission: admission, execution: execution}
			clock := &ownerTestClock{}
			queue := &ownerTestQueue{}
			var fired []int64
			owner := newTestOwner(t, config, clock, queue, &fired)
			for _, tag := range []int64{*tc.Tag, *tc.Tag + 1} {
				if _, err := owner.Toggle(1, tag); err != nil {
					t.Fatal(err)
				}
			}
			for len(queue.work) > 0 {
				queue.runNext()
			}
			if len(clock.timers) != 2 {
				t.Fatalf("preseed timers=%d", len(clock.timers))
			}
			if _, err := owner.Toggle(enableByte, *tc.Tag); err != nil {
				t.Fatal(err)
			}
			queue.runNext()
			if !clock.timers[0].stopped || clock.timers[1].stopped {
				t.Fatalf("stopped=[%t %t], want only matching tag stopped", clock.timers[0].stopped, clock.timers[1].stopped)
			}
		})
	}
}

func TestReceiveHeaderTimeoutOwnerCloseSuppressesQueuedAndTimerDelivery(t *testing.T) {
	config := &ownerTestConfig{admission: time.Second, execution: time.Second}
	clock := &ownerTestClock{}
	queue := &ownerTestQueue{}
	var fired []int64
	owner := newTestOwner(t, config, clock, queue, &fired)
	if _, err := owner.Toggle(1, 7); err != nil {
		t.Fatal(err)
	}
	owner.Close()
	queue.runNext()
	if len(clock.timers) != 0 {
		t.Fatalf("closed owner scheduled timers=%d", len(clock.timers))
	}
	if !reflect.DeepEqual(fired, []int64(nil)) {
		t.Fatalf("fired=%v", fired)
	}
}

func TestReceiveHeaderTimeoutOwnerCanceledQueuedZeroTagCannotReplay(t *testing.T) {
	config := &ownerTestConfig{admission: time.Second, execution: time.Second}
	clock := &ownerTestClock{}
	queue := &ownerTestQueue{}
	var fired []int64
	owner := newTestOwner(t, config, clock, queue, &fired)
	if _, err := owner.Toggle(1, 0); err != nil {
		t.Fatal(err)
	}
	enable := queue.work[0]
	if _, err := owner.Toggle(0, 0); err != nil {
		t.Fatal(err)
	}
	queue.runNext()
	if len(clock.timers) != 1 {
		t.Fatalf("initial zero-tag enable scheduled timers=%d", len(clock.timers))
	}
	queue.runNext()
	if !clock.timers[0].stopped {
		t.Fatalf("zero-tag disable did not stop initial timer")
	}
	// Replaying the already-consumed enable closure must be a no-op. A plain
	// map lookup would treat the missing zero-valued token as a valid match.
	enable()
	if len(clock.timers) != 1 {
		t.Fatalf("replayed canceled zero-tag queue scheduled timers=%d", len(clock.timers))
	}
}

type receiveHeaderTimeoutContract struct {
	Status    string                     `json:"status"`
	Questions []string                   `json:"questions"`
	Cases     []receiveHeaderTimeoutCase `json:"cases"`
}
type receiveHeaderTimeoutCase struct {
	Name                    string   `json:"name"`
	Kind                    string   `json:"kind"`
	Evidence                []string `json:"evidence"`
	TimeoutSeconds          *float64 `json:"timeout_seconds"`
	ExecutionTimeoutSeconds *float64 `json:"execution_timeout_seconds"`
	HandlerPresent          *bool    `json:"handler_present"`
	OldStatus               *int8    `json:"old_status"`
	NewStatus               *int8    `json:"new_status"`
	Tag                     *int64   `json:"tag"`
	Enable                  *bool    `json:"enable"`
	EnableByte              *uint8   `json:"enable_byte"`
	PacketID                *uint32  `json:"packet_id"`
	ExpectedTag             *int64   `json:"expected_tag"`
	PacketMethod            *string  `json:"packet_method"`
	ExpectedUniqueID        *string  `json:"expected_unique_id"`
	StoredUniqueID          *string  `json:"stored_unique_id"`
	IncomingUniqueID        *string  `json:"incoming_unique_id"`
	ProducerStatus          *uint8   `json:"producer_status"`
	CompletionPresent       *bool    `json:"completion_present"`
	Expect                  []string `json:"expect"`
	RemainingGaps           []string `json:"remaining_gaps"`
}

var knownReceiveHeaderTimeoutEffect = map[string]bool{"read_timeout": true, "check_tag_nonnegative": true, "queue_main": true, "no_enqueue": true, "reread_timeout": true, "perform_selector_after_delay_0": true, "perform_selector_after_delay_7": true, "owner_target": true, "fire_selector": true, "wrapped_tag": true, "cancel_previous_perform": true, "write_status_byte_zero": true, "set_status_zero": true, "invoke_status_handler_old_new_error": true, "no_status_handler": true, "cancel_owner_delayed_work": true, "enumerate_pending": true, "completion_nil": true, "error_domain_locoagent": true, "error_code_minus_one": true, "error_userinfo_nil": true, "lookup_unsigned_packet_id": true, "compare_stored_string_to_incoming_unique_id": true, "derive_request_tag": true, "derive_request_tag_identity": true, "disable_timeout": true, "comparison_false": true, "no_timeout_action": true, "register_completion_by_unique_id": true, "store_unique_id_by_packet_id": true, "send_packet": true, "arm_timeout": true, "forward_error": true, "no_timeout_arm": true, "no_callback": true, "construct_unique_id": true}

func loadReceiveHeaderTimeoutContract(path string) (receiveHeaderTimeoutContract, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return receiveHeaderTimeoutContract{}, err
	}
	var v receiveHeaderTimeoutContract
	if err := json.Unmarshal(body, &v); err != nil {
		return v, err
	}
	if len(v.Cases) == 0 {
		return v, fmt.Errorf("no cases in %s", path)
	}
	return v, nil
}
