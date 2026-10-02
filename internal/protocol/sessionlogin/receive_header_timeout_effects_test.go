package sessionlogin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
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

func TestPlanReceiveHeaderTimeoutApprovedVectors(t *testing.T) {
	type vectorCase struct {
		Name              string   `json:"name"`
		Kind              string   `json:"kind"`
		Evidence          []string `json:"evidence"`
		TimeoutSeconds    *float64 `json:"timeout_seconds"`
		ExecutionTimeout  *float64 `json:"execution_timeout_seconds"`
		Tag               *int64   `json:"tag"`
		Enable            *bool    `json:"enable"`
		EnableByte        *byte    `json:"enable_byte"`
		Expect            []string `json:"expect"`
		RemainingGaps     []string `json:"remaining_gaps"`
		PacketID          *uint32  `json:"packet_id"`
		StoredUniqueID    string   `json:"stored_unique_id"`
		IncomingUniqueID  string   `json:"incoming_unique_id"`
		ProducerStatus    *int32   `json:"producer_status"`
		CompletionPresent *bool    `json:"completion_present"`
		HandlerPresent    *bool    `json:"handler_present"`
		NewStatus         *int32   `json:"new_status"`
		OldStatus         *int32   `json:"old_status"`
	}
	var vectors struct {
		Status    string       `json:"status"`
		Questions []string     `json:"questions"`
		Cases     []vectorCase `json:"cases"`
	}
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-timeout-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&vectors); err != nil {
		t.Fatal(err)
	}
	if vectors.Status != "reviewed-static-unexecuted-runtime" || len(vectors.Cases) < 8 {
		t.Fatalf("unexpected timeout vector status/cases: %q/%d", vectors.Status, len(vectors.Cases))
	}
	known := map[string]bool{"no_enqueue": true, "read_timeout": true, "check_tag_nonnegative": true, "queue_main": true, "reread_timeout": true, "perform_selector_after_delay": true, "cancel_previous_perform": true, "owner_target": true, "fire_selector": true, "wrapped_tag": true}
	for _, tc := range vectors.Cases {
		if tc.Kind != "timeout-admission" && tc.Kind != "timeout-enable" && tc.Kind != "timeout-disable" {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			if len(tc.Evidence) == 0 || tc.TimeoutSeconds == nil || tc.Tag == nil {
				t.Fatal("missing source evidence or admission fields")
			}
			admission, conversionErr := timeoutVectorDuration(tc.TimeoutSeconds)
			if conversionErr != nil {
				t.Fatal(conversionErr)
			}
			input := ReceiveHeaderTimeoutInput{AdmissionTimeout: admission, Owner: "agent", RequestTag: *tc.Tag}
			if tc.ExecutionTimeout != nil {
				input.ExecutionTimeout, conversionErr = timeoutVectorDuration(tc.ExecutionTimeout)
				if conversionErr != nil {
					t.Fatal(conversionErr)
				}
			}
			if tc.EnableByte != nil {
				input.EnableByte = *tc.EnableByte
			} else if tc.Enable != nil && *tc.Enable {
				input.EnableByte = 1
			}
			got, err := PlanReceiveHeaderTimeout(input)
			if err != nil {
				t.Fatal(err)
			}
			for _, expected := range tc.Expect {
				sourceExpected := expected
				if strings.HasPrefix(sourceExpected, "perform_selector_after_delay_") {
					expected = "perform_selector_after_delay"
				}
				if !known[expected] {
					t.Fatalf("unknown source effect %q", sourceExpected)
				}
				found := false
				for i, effect := range got {
					if effect.Kind == expected {
						if strings.HasPrefix(sourceExpected, "perform_selector_after_delay_") {
							seconds, parseErr := strconv.ParseFloat(strings.TrimPrefix(sourceExpected, "perform_selector_after_delay_"), 64)
							expectedDelay, delayErr := timeoutVectorDuration(&seconds)
							if parseErr != nil || delayErr != nil || effect.Delay != expectedDelay {
								continue
							}
						}
						if err := assertTimeoutVectorEffect(sourceExpected, effect, tc.Tag); err != nil {
							t.Fatal(err)
						}
						got = got[i+1:]
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("expected ordered source effect %q in %#v", expected, got)
				}
			}
		})
	}
}

func timeoutVectorDuration(seconds *float64) (time.Duration, error) {
	if seconds == nil {
		return 0, nil
	}
	if math.IsNaN(*seconds) || math.IsInf(*seconds, 0) {
		return 0, fmt.Errorf("timeout vector duration must be finite: %v", *seconds)
	}
	nanos := *seconds * float64(time.Second)
	if nanos < -float64(1<<63) || nanos > float64(1<<63-1) {
		return 0, fmt.Errorf("timeout vector duration overflows time.Duration: %v seconds", *seconds)
	}
	return time.Duration(math.Round(nanos)), nil
}

func assertTimeoutVectorEffect(sourceExpected string, effect ReceiveHeaderTimeoutEffect, tag *int64) error {
	switch {
	case strings.HasPrefix(sourceExpected, "perform_selector_after_delay_"):
		if effect.Owner != "agent" || effect.Target != receiveHeaderTimeoutSelector || tag == nil || effect.Tag != *tag {
			return fmt.Errorf("%s has incomplete owner/target/tag tuple: %#v", sourceExpected, effect)
		}
	case sourceExpected == "cancel_previous_perform":
		if effect.Owner != "agent" || effect.Target != receiveHeaderTimeoutSelector || tag == nil || effect.Tag != *tag {
			return fmt.Errorf("%s has incomplete owner/target/tag tuple: %#v", sourceExpected, effect)
		}
	case sourceExpected == "owner_target":
		if effect.Owner != "agent" || effect.Target != "" {
			return fmt.Errorf("owner_target has unexpected tuple: %#v", effect)
		}
	case sourceExpected == "fire_selector":
		if effect.Target != receiveHeaderTimeoutSelector {
			return fmt.Errorf("fire_selector target=%q want %q", effect.Target, receiveHeaderTimeoutSelector)
		}
	case sourceExpected == "wrapped_tag":
		if tag == nil || effect.Tag != *tag {
			return fmt.Errorf("wrapped_tag=%d want %v", effect.Tag, tag)
		}
	}
	return nil
}
