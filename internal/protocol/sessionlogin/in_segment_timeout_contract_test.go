package sessionlogin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type inSegmentTimeoutContract struct {
	Status string                 `json:"status"`
	Cases  []inSegmentTimeoutCase `json:"cases"`
}

type inSegmentTimeoutCase struct {
	Name             string   `json:"name"`
	Kind             string   `json:"kind"`
	Evidence         []string `json:"evidence"`
	AdmissionTimeout *float64 `json:"admission_timeout_seconds"`
	ExecutionTimeout *float64 `json:"execution_timeout_seconds"`
	EnableByte       *int     `json:"enable_byte"`
	Expect           []string `json:"expect"`
	ExpectedDelay    *float64 `json:"expected_schedule_delay_seconds"`
	RemainingGaps    []string `json:"remaining_gaps"`
}

var inSegmentTimeoutKinds = map[string]bool{"toggle": true, "fire": true}

var inSegmentTimeoutEffects = map[string]bool{
	"skip_main_queue_toggle":               true,
	"queue_main_queue_toggle":              true,
	"reread_timeout_only_on_enable":        true,
	"schedule_fire_in_segment_timeout_nil": true,
	"cancel_exact_owner_selector_nil":      true,
	"disconnect_owner":                     true,
}

func loadInSegmentTimeoutContract(path string) (inSegmentTimeoutContract, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return inSegmentTimeoutContract{}, err
	}
	var contract inSegmentTimeoutContract
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&contract); err != nil {
		return contract, err
	}
	if err := validateInSegmentTimeoutContract(contract); err != nil {
		return contract, err
	}
	return contract, nil
}

func validateInSegmentTimeoutContract(contract inSegmentTimeoutContract) error {
	if contract.Status != "reviewed-static-unexecuted-runtime" || len(contract.Cases) != 6 {
		return fmt.Errorf("header cases=%d status=%q", len(contract.Cases), contract.Status)
	}
	seen := map[string]bool{}
	for _, tc := range contract.Cases {
		if tc.Name == "" || seen[tc.Name] || !inSegmentTimeoutKinds[tc.Kind] || len(tc.Evidence) == 0 || len(tc.Expect) == 0 {
			return fmt.Errorf("invalid case %q", tc.Name)
		}
		seen[tc.Name] = true
		for _, evidence := range tc.Evidence {
			if evidence != "RC-BIN-030" {
				return fmt.Errorf("unknown evidence %q", evidence)
			}
		}
		for _, effect := range tc.Expect {
			if !inSegmentTimeoutEffects[effect] {
				return fmt.Errorf("unknown effect %q", effect)
			}
		}
		if tc.Kind == "fire" {
			if tc.AdmissionTimeout != nil || tc.ExecutionTimeout != nil || tc.EnableByte != nil || !reflect.DeepEqual(tc.Expect, []string{"disconnect_owner"}) {
				return fmt.Errorf("invalid fire case %q", tc.Name)
			}
			continue
		}
		if tc.AdmissionTimeout == nil || tc.EnableByte == nil {
			return fmt.Errorf("missing toggle inputs %q", tc.Name)
		}
		var want []string
		if *tc.AdmissionTimeout <= 0 {
			want = []string{"skip_main_queue_toggle"}
		} else {
			want = []string{"queue_main_queue_toggle"}
			if *tc.EnableByte == 1 {
				if tc.ExecutionTimeout == nil || tc.ExpectedDelay == nil || *tc.ExpectedDelay != *tc.ExecutionTimeout {
					return fmt.Errorf("enable case must forward reread delay %q", tc.Name)
				}
				want = append(want, "reread_timeout_only_on_enable", "schedule_fire_in_segment_timeout_nil")
			} else {
				if tc.ExecutionTimeout != nil || tc.ExpectedDelay != nil {
					return fmt.Errorf("disable case must not reread timeout %q", tc.Name)
				}
				want = append(want, "cancel_exact_owner_selector_nil")
			}
		}
		if !reflect.DeepEqual(tc.Expect, want) {
			return fmt.Errorf("%s effects=%v want=%v", tc.Name, tc.Expect, want)
		}
	}
	return nil
}

func TestInSegmentTimeoutContractSchema(t *testing.T) {
	if _, err := loadInSegmentTimeoutContract(filepath.Join("testdata", "reconnect", "rc-q5-in-segment-timeout.json")); err != nil {
		t.Fatal(err)
	}
}
