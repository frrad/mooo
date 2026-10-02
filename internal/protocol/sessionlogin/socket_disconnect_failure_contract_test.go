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

type socketDisconnectFailureContract struct {
	Status string                        `json:"status"`
	Cases  []socketDisconnectFailureCase `json:"cases"`
}

type socketDisconnectFailureCase struct {
	Name                           string   `json:"name"`
	Kind                           string   `json:"kind"`
	Evidence                       []string `json:"evidence"`
	HandlerPresent                 *bool    `json:"handler_present"`
	PendingCount                   *int     `json:"pending_count"`
	ErrorDomain                    string   `json:"error_domain"`
	ErrorCode                      *int     `json:"error_code"`
	ErrorUserInfoNil               *bool    `json:"error_userinfo_nil"`
	CompletionCalls                *int     `json:"completion_calls"`
	SuppliedErrorDomain            string   `json:"supplied_error_domain"`
	SuppliedErrorCode              *int     `json:"supplied_error_code"`
	SuppliedErrorUserInfoNil       *bool    `json:"supplied_error_userinfo_nil"`
	ExpectedFanoutErrorDomain      string   `json:"expected_fanout_error_domain"`
	ExpectedFanoutErrorCode        *int     `json:"expected_fanout_error_code"`
	ExpectedFanoutErrorUserInfoNil *bool    `json:"expected_fanout_error_userinfo_nil"`
	Expect                         []string `json:"expect"`
	RemainingGaps                  []string `json:"remaining_gaps"`
}

var socketDisconnectFailureKinds = map[string]bool{
	"socket-disconnect-fanout": true,
	"socket-disconnect-empty":  true,
	"helper-fanout-and-clear":  true,
}

var socketDisconnectFailureEffects = map[string]bool{
	"set_status_zero_with_socket_error":    true,
	"invoke_status_handler_before_cleanup": true,
	"no_status_handler_invocation":         true,
	"cancel_delayed_work_for_agent_target": true,
	"enumerate_pending_completion_map":     true,
	"enumerate_empty_pending_map":          true,
	"fanout_nil_plus_locoagent_error":      true,
	"helper_enumerate_pending_map":         true,
	"helper_fanout_supplied_error":         true,
	"helper_clear_completion_map":          true,
	"helper_clear_packet_identity_map":     true,
}

func loadSocketDisconnectFailureContract(path string) (socketDisconnectFailureContract, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return socketDisconnectFailureContract{}, err
	}
	var contract socketDisconnectFailureContract
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&contract); err != nil {
		return contract, err
	}
	if err := validateSocketDisconnectFailureContract(contract); err != nil {
		return contract, err
	}
	return contract, nil
}

func validateSocketDisconnectFailureContract(contract socketDisconnectFailureContract) error {
	if contract.Status != "reviewed-static-unexecuted-runtime" || len(contract.Cases) != 4 {
		return fmt.Errorf("header cases=%d status=%q", len(contract.Cases), contract.Status)
	}
	seen := map[string]bool{}
	for _, tc := range contract.Cases {
		if tc.Name == "" || seen[tc.Name] || !socketDisconnectFailureKinds[tc.Kind] || len(tc.Evidence) == 0 || len(tc.Expect) == 0 {
			return fmt.Errorf("invalid case %q", tc.Name)
		}
		seen[tc.Name] = true
		for _, evidence := range tc.Evidence {
			if evidence != "RC-BIN-016" && evidence != "RC-BIN-029" {
				return fmt.Errorf("unknown evidence %q", evidence)
			}
		}
		for _, effect := range tc.Expect {
			if !socketDisconnectFailureEffects[effect] {
				return fmt.Errorf("unknown effect %q", effect)
			}
		}
		switch tc.Kind {
		case "socket-disconnect-fanout", "socket-disconnect-empty":
			if tc.HandlerPresent == nil || tc.PendingCount == nil || *tc.PendingCount < 0 || tc.ErrorDomain != "LocoAgent" || tc.ErrorCode == nil || *tc.ErrorCode != -1 || tc.ErrorUserInfoNil == nil || !*tc.ErrorUserInfoNil {
				return fmt.Errorf("invalid socket disconnect inputs %q", tc.Name)
			}
			if tc.CompletionCalls == nil || *tc.CompletionCalls != *tc.PendingCount {
				return fmt.Errorf("completion count does not match pending count %q", tc.Name)
			}
			if tc.ExpectedFanoutErrorDomain != tc.ErrorDomain || tc.ExpectedFanoutErrorCode == nil || *tc.ExpectedFanoutErrorCode != *tc.ErrorCode || tc.ExpectedFanoutErrorUserInfoNil == nil || *tc.ExpectedFanoutErrorUserInfoNil != *tc.ErrorUserInfoNil {
				return fmt.Errorf("socket fanout error tuple mismatch %q", tc.Name)
			}
		case "helper-fanout-and-clear":
			if tc.PendingCount == nil || *tc.PendingCount < 0 || tc.SuppliedErrorDomain == "" || tc.SuppliedErrorCode == nil || tc.SuppliedErrorUserInfoNil == nil || tc.CompletionCalls == nil || *tc.CompletionCalls != *tc.PendingCount {
				return fmt.Errorf("invalid helper inputs %q", tc.Name)
			}
			if tc.ExpectedFanoutErrorDomain != tc.SuppliedErrorDomain || tc.ExpectedFanoutErrorCode == nil || *tc.ExpectedFanoutErrorCode != *tc.SuppliedErrorCode || tc.ExpectedFanoutErrorUserInfoNil == nil || *tc.ExpectedFanoutErrorUserInfoNil != *tc.SuppliedErrorUserInfoNil {
				return fmt.Errorf("helper fanout error tuple mismatch %q", tc.Name)
			}
		}
	}
	return nil
}

func TestSocketDisconnectFailureContractSchema(t *testing.T) {
	contract, err := loadSocketDisconnectFailureContract(filepath.Join("testdata", "reconnect", "rc-q5-socket-disconnect-failure.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range contract.Cases {
		var want []string
		switch tc.Kind {
		case "socket-disconnect-fanout", "socket-disconnect-empty":
			want = []string{"set_status_zero_with_socket_error"}
			if *tc.HandlerPresent {
				want = append(want, "invoke_status_handler_before_cleanup")
			} else {
				want = append(want, "no_status_handler_invocation")
			}
			want = append(want, "cancel_delayed_work_for_agent_target")
			if *tc.PendingCount == 0 {
				want = append(want, "enumerate_empty_pending_map")
			} else {
				want = append(want, "enumerate_pending_completion_map", "fanout_nil_plus_locoagent_error")
			}
		case "helper-fanout-and-clear":
			want = []string{"helper_enumerate_pending_map"}
			if *tc.PendingCount > 0 {
				want = append(want, "helper_fanout_supplied_error")
			}
			want = append(want, "helper_clear_completion_map", "helper_clear_packet_identity_map")
		}
		if !reflect.DeepEqual(tc.Expect, want) {
			t.Fatalf("%s order=%v want=%v", tc.Name, tc.Expect, want)
		}
	}
}
