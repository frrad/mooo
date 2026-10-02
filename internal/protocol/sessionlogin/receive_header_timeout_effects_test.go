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

func TestPlanReceiveHeaderTimeoutApprovedAdmissionVectors(t *testing.T) {
	type vector struct {
		name, evidence string
		input          ReceiveHeaderTimeoutInput
		want           []string
	}
	allKinds := map[string]bool{
		"no_enqueue": true, "read_timeout": true, "check_tag_nonnegative": true,
		"queue_main": true, "reread_timeout": true, "perform_selector_after_delay": true,
		"cancel_previous_perform": true, "owner_target": true, "fire_selector": true,
		"wrapped_tag": true,
	}
	vectors := []vector{
		{"positive timeout tag zero", "RC-BIN-010", ReceiveHeaderTimeoutInput{AdmissionTimeout: 20 * time.Second, EnableByte: 1, Owner: "agent", RequestTag: 0}, []string{"read_timeout", "check_tag_nonnegative", "queue_main", "reread_timeout", "perform_selector_after_delay", "owner_target", "fire_selector", "wrapped_tag"}},
		{"positive timeout maximum signed tag", "RC-BIN-010", ReceiveHeaderTimeoutInput{AdmissionTimeout: 20 * time.Second, Owner: "agent", RequestTag: 1<<63 - 1}, []string{"read_timeout", "check_tag_nonnegative", "queue_main", "cancel_previous_perform", "owner_target", "fire_selector", "wrapped_tag"}},
		{"zero timeout rejects zero tag", "RC-BIN-010", ReceiveHeaderTimeoutInput{Owner: "agent", RequestTag: 0}, []string{"no_enqueue"}},
		{"negative timeout rejects zero tag", "RC-BIN-010", ReceiveHeaderTimeoutInput{AdmissionTimeout: -time.Second, Owner: "agent", RequestTag: 0}, []string{"no_enqueue"}},
		{"positive timeout rejects minimum signed tag", "RC-BIN-010", ReceiveHeaderTimeoutInput{AdmissionTimeout: 20 * time.Second, Owner: "agent", RequestTag: -1 << 63}, []string{"no_enqueue"}},
		{"enable rereads changed timeout", "RC-BIN-010", ReceiveHeaderTimeoutInput{AdmissionTimeout: 20 * time.Second, ExecutionTimeout: 7 * time.Second, EnableByte: 1, Owner: "agent", RequestTag: 1}, []string{"read_timeout", "check_tag_nonnegative", "queue_main", "reread_timeout", "perform_selector_after_delay", "owner_target", "fire_selector", "wrapped_tag"}},
		{"disable false uses cancellation tuple", "RC-BIN-010", ReceiveHeaderTimeoutInput{AdmissionTimeout: 20 * time.Second, ExecutionTimeout: 7 * time.Second, Owner: "agent", RequestTag: 1}, []string{"read_timeout", "check_tag_nonnegative", "queue_main", "cancel_previous_perform", "owner_target", "fire_selector", "wrapped_tag"}},
		{"enable byte two follows non-enable branch", "RC-BIN-010", ReceiveHeaderTimeoutInput{AdmissionTimeout: 20 * time.Second, ExecutionTimeout: 7 * time.Second, EnableByte: 2, Owner: "agent", RequestTag: 1}, []string{"read_timeout", "check_tag_nonnegative", "queue_main", "cancel_previous_perform", "owner_target", "fire_selector", "wrapped_tag"}},
		{"execution timeout becomes zero", "RC-BIN-010", ReceiveHeaderTimeoutInput{AdmissionTimeout: 20 * time.Second, EnableByte: 1, Owner: "agent", RequestTag: 1}, []string{"read_timeout", "check_tag_nonnegative", "queue_main", "reread_timeout", "perform_selector_after_delay", "owner_target", "fire_selector", "wrapped_tag"}},
	}
	for _, tc := range vectors {
		t.Run(tc.name, func(t *testing.T) {
			if tc.evidence == "" {
				t.Fatal("missing source evidence")
			}
			got, err := PlanReceiveHeaderTimeout(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			kinds := make([]string, len(got))
			for i, effect := range got {
				if !allKinds[effect.Kind] {
					t.Fatalf("unknown effect kind %q", effect.Kind)
				}
				kinds[i] = effect.Kind
			}
			if !reflect.DeepEqual(kinds, tc.want) {
				t.Fatalf("effect kinds=%v want=%v", kinds, tc.want)
			}
		})
	}
}
