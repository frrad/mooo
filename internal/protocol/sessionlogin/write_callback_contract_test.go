package sessionlogin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type writeCallbackContract struct {
	Status string              `json:"status"`
	Cases  []writeCallbackCase `json:"cases"`
}

type writeCallbackCase struct {
	Name                    string   `json:"name"`
	Class                   string   `json:"class"`
	Callback                string   `json:"callback"`
	TimeoutAdmissionSeconds float64  `json:"timeout_admission_seconds"`
	TimeoutExecutionSeconds float64  `json:"timeout_execution_seconds"`
	EnableByte              int      `json:"enable_byte"`
	TimeoutOwner            string   `json:"timeout_owner"`
	TimeoutSelector         string   `json:"timeout_selector"`
	TimeoutObjectNil        *bool    `json:"timeout_object_nil"`
	ExpectedScheduledDelay  *float64 `json:"expected_scheduled_delay_seconds"`
	ExpectedEffects         []string `json:"expected_effects"`
	RemainingGaps           []string `json:"remaining_gaps"`
}

var knownWriteCallbackEffects = map[string]bool{
	"disable_out_segment_timeout":           true,
	"invoke_empty_did_write":                true,
	"read_timeout_for_admission":            true,
	"queue_main_timeout_block":              true,
	"reread_timeout_for_execution":          true,
	"schedule_timeout_selector_with_reread": true,
	"cancel_exact_timeout_tuple":            true,
	"skip_timeout_enqueue":                  true,
}

func loadWriteCallbackContract(path string) (writeCallbackContract, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return writeCallbackContract{}, err
	}
	var contract writeCallbackContract
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&contract); err != nil {
		return contract, err
	}
	if contract.Status != "reviewed-static-unexecuted-runtime" {
		return contract, fmt.Errorf("status=%q", contract.Status)
	}
	if len(contract.Cases) != 9 {
		return contract, fmt.Errorf("cases=%d", len(contract.Cases))
	}
	seen := map[string]bool{}
	for _, tc := range contract.Cases {
		if tc.Name == "" || seen[tc.Name] || tc.Class != "LocoAgent" || len(tc.ExpectedEffects) == 0 || len(tc.RemainingGaps) == 0 {
			return contract, fmt.Errorf("invalid case %q", tc.Name)
		}
		seen[tc.Name] = true
		for _, effect := range tc.ExpectedEffects {
			if !knownWriteCallbackEffects[effect] {
				return contract, fmt.Errorf("unknown effect %q in %q", effect, tc.Name)
			}
		}
		var want []string
		switch tc.Callback {
		case "partial":
			want = []string{"disable_out_segment_timeout"}
		case "complete":
			want = []string{"disable_out_segment_timeout", "invoke_empty_did_write"}
		case "timeout-toggle":
			if tc.EnableByte < 0 || tc.EnableByte > 255 {
				return contract, fmt.Errorf("enable byte out of range %d", tc.EnableByte)
			}
			if tc.TimeoutOwner != "same-agent" || tc.TimeoutSelector != "fireOutSegmentTimeout" || tc.TimeoutObjectNil == nil || !*tc.TimeoutObjectNil {
				return contract, fmt.Errorf("invalid timeout tuple in %q", tc.Name)
			}
			want = []string{"read_timeout_for_admission"}
			if tc.TimeoutAdmissionSeconds <= 0 {
				want = append(want, "skip_timeout_enqueue")
				if tc.ExpectedScheduledDelay != nil {
					return contract, fmt.Errorf("unexpected scheduled delay in %q", tc.Name)
				}
			} else {
				want = append(want, "queue_main_timeout_block")
				if tc.EnableByte == 1 {
					if tc.ExpectedScheduledDelay == nil || *tc.ExpectedScheduledDelay != tc.TimeoutExecutionSeconds {
						return contract, fmt.Errorf("scheduled delay mismatch in %q", tc.Name)
					}
					want = append(want, "reread_timeout_for_execution", "schedule_timeout_selector_with_reread")
				} else {
					if tc.ExpectedScheduledDelay != nil {
						return contract, fmt.Errorf("unexpected scheduled delay in %q", tc.Name)
					}
					want = append(want, "cancel_exact_timeout_tuple")
				}
			}
		default:
			return contract, fmt.Errorf("unknown callback %q", tc.Callback)
		}
		if len(want) != len(tc.ExpectedEffects) {
			return contract, fmt.Errorf("effect count mismatch %q: got=%v want=%v", tc.Name, tc.ExpectedEffects, want)
		}
		for i := range want {
			if tc.ExpectedEffects[i] != want[i] {
				return contract, fmt.Errorf("effect order mismatch %q: got=%v want=%v", tc.Name, tc.ExpectedEffects, want)
			}
		}
	}
	return contract, nil
}

func TestWriteCallbackContractSchema(t *testing.T) {
	contract, err := loadWriteCallbackContract(filepath.Join("testdata", "reconnect", "rc-q5-write-callbacks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.Cases) != 9 {
		t.Fatalf("cases=%d", len(contract.Cases))
	}
}
