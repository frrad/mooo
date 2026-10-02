package sessionlogin

import (
	"reflect"
	"testing"
	"time"
)

func TestPlanReceiveHeaderTimeoutAdmissionAndQueuedEnable(t *testing.T) {
	got, err := PlanReceiveHeaderTimeout(ReceiveHeaderTimeoutInput{
		AdmissionTimeout: 20 * time.Second, ExecutionTimeout: 1500 * time.Millisecond,
		EnableByte: 1, Owner: "agent", RequestTag: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []ReceiveHeaderTimeoutEffect{
		{Kind: "read_timeout", Delay: 20 * time.Second},
		{Kind: "check_tag_nonnegative", Tag: 7}, {Kind: "queue_main"},
		{Kind: "reread_timeout", Delay: 1500 * time.Millisecond},
		{Kind: "perform_selector_after_delay", Delay: 1500 * time.Millisecond, Owner: "agent", Target: receiveHeaderTimeoutSelector, Tag: 7},
		{Kind: "owner_target", Owner: "agent"},
		{Kind: "fire_selector", Target: receiveHeaderTimeoutSelector},
		{Kind: "wrapped_tag", Tag: 7},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects=%#v want=%#v", got, want)
	}
}

func TestPlanReceiveHeaderTimeoutDisableUsesExactTuple(t *testing.T) {
	got, err := PlanReceiveHeaderTimeout(ReceiveHeaderTimeoutInput{
		AdmissionTimeout: 20 * time.Second, ExecutionTimeout: 0,
		EnableByte: 2, Owner: "agent", RequestTag: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[len(got)-4] != (ReceiveHeaderTimeoutEffect{Kind: "cancel_previous_perform", Owner: "agent", Target: receiveHeaderTimeoutSelector, Tag: 1}) || got[len(got)-3] != (ReceiveHeaderTimeoutEffect{Kind: "owner_target", Owner: "agent"}) || got[len(got)-2] != (ReceiveHeaderTimeoutEffect{Kind: "fire_selector", Target: receiveHeaderTimeoutSelector}) || got[len(got)-1] != (ReceiveHeaderTimeoutEffect{Kind: "wrapped_tag", Tag: 1}) {
		t.Fatalf("disable effects=%#v", got)
	}
}

func TestPlanReceiveHeaderTimeoutEnableUsesExecutionTimeZero(t *testing.T) {
	got, err := PlanReceiveHeaderTimeout(ReceiveHeaderTimeoutInput{AdmissionTimeout: 20 * time.Second, EnableByte: 1, Owner: "agent", RequestTag: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got[3].Kind != "reread_timeout" || got[3].Delay != 0 || got[4].Kind != "perform_selector_after_delay" || got[4].Delay != 0 || got[4].Owner != "agent" || got[4].Target != receiveHeaderTimeoutSelector || got[4].Tag != 1 {
		t.Fatalf("zero execution timeout effects=%#v", got)
	}
}

func TestPlanReceiveHeaderTimeoutAdmissionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   ReceiveHeaderTimeoutInput
	}{
		{"zero timeout", ReceiveHeaderTimeoutInput{AdmissionTimeout: 0, Owner: "agent", RequestTag: 0}},
		{"negative timeout", ReceiveHeaderTimeoutInput{AdmissionTimeout: -time.Nanosecond, Owner: "agent", RequestTag: 0}},
		{"negative tag", ReceiveHeaderTimeoutInput{AdmissionTimeout: time.Nanosecond, Owner: "agent", RequestTag: -1}},
		{"minimum tag", ReceiveHeaderTimeoutInput{AdmissionTimeout: time.Nanosecond, Owner: "agent", RequestTag: -1 << 63}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PlanReceiveHeaderTimeout(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, []ReceiveHeaderTimeoutEffect{{Kind: "no_enqueue"}}) {
				t.Fatalf("effects=%#v", got)
			}
		})
	}
	max, err := PlanReceiveHeaderTimeout(ReceiveHeaderTimeoutInput{AdmissionTimeout: time.Nanosecond, EnableByte: 1, Owner: "agent", RequestTag: 1<<63 - 1})
	if err != nil || len(max) != 8 {
		t.Fatalf("maximum tag effects=%#v err=%v", max, err)
	}
}

func TestPlanReceiveHeaderTimeoutRejectsMissingOwner(t *testing.T) {
	if got, err := PlanReceiveHeaderTimeout(ReceiveHeaderTimeoutInput{AdmissionTimeout: time.Second}); err == nil || got != nil {
		t.Fatalf("missing owner got=%#v err=%v", got, err)
	}
}
