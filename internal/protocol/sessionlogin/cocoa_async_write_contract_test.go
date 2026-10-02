package sessionlogin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type cocoaAsyncWriteContract struct {
	Status string                `json:"status"`
	Cases  []cocoaAsyncWriteCase `json:"cases"`
}

type cocoaAsyncWriteCase struct {
	Name                  string   `json:"name"`
	Kind                  string   `json:"kind"`
	DataLength            int      `json:"data_length"`
	WriteDeltas           []int    `json:"write_deltas"`
	Tag                   int64    `json:"tag"`
	ReceiveTimeoutEnabled *bool    `json:"receive_timeout_enabled"`
	ExpectedSubmit        []string `json:"expected_submit_events"`
	ExpectedSocket        []string `json:"expected_socket_events"`
	ExpectedDelegate      []string `json:"expected_delegate_events"`
}

func loadCocoaAsyncWriteContract(path string) (cocoaAsyncWriteContract, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return cocoaAsyncWriteContract{}, err
	}
	var contract cocoaAsyncWriteContract
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&contract); err != nil {
		return contract, err
	}
	if contract.Status != "reviewed-static-unexecuted-runtime" || len(contract.Cases) != 6 {
		return contract, fmt.Errorf("invalid status=%q cases=%d", contract.Status, len(contract.Cases))
	}
	seen := map[string]bool{}
	for _, tc := range contract.Cases {
		if tc.Name == "" || seen[tc.Name] || tc.DataLength < 0 || len(tc.ExpectedSubmit) == 0 {
			return contract, fmt.Errorf("invalid case %q", tc.Name)
		}
		seen[tc.Name] = true
		var submit, socket, delegate []string
		switch tc.Kind {
		case "empty_write":
			if tc.DataLength != 0 || len(tc.WriteDeltas) != 0 || tc.ReceiveTimeoutEnabled != nil {
				return contract, fmt.Errorf("invalid empty write %q", tc.Name)
			}
			submit = []string{"writeData_returns_without_enqueue"}
		case "partial_write":
			if tc.DataLength == 0 || len(tc.WriteDeltas) == 0 || tc.ReceiveTimeoutEnabled == nil || !*tc.ReceiveTimeoutEnabled {
				return contract, fmt.Errorf("invalid partial write %q", tc.Name)
			}
			submit = []string{"writeData_dispatches_internal_write_queue", "out_timeout_enable_after_writeData_returns", "receive_header_timeout_enable_after_writeData_returns"}
			remaining := tc.DataLength
			for _, delta := range tc.WriteDeltas {
				if delta <= 0 || delta >= remaining {
					return contract, fmt.Errorf("invalid partial delta %q", tc.Name)
				}
				remaining -= delta
				socket = append(socket, "write_progress")
				delegate = append(delegate, fmt.Sprintf("partial_callback_socket_bytes_tag:%d:%d", delta, tc.Tag))
			}
			if remaining == 0 {
				return contract, fmt.Errorf("partial case completes write %q", tc.Name)
			}
			socket = append([]string{"write_packet_enqueue"}, socket...)
		case "partial_then_complete":
			if tc.DataLength == 0 || len(tc.WriteDeltas) < 2 || tc.ReceiveTimeoutEnabled == nil || !*tc.ReceiveTimeoutEnabled {
				return contract, fmt.Errorf("invalid partial completion %q", tc.Name)
			}
			submit = []string{"writeData_dispatches_internal_write_queue", "out_timeout_enable_after_writeData_returns", "receive_header_timeout_enable_after_writeData_returns"}
			remaining := tc.DataLength
			for i, delta := range tc.WriteDeltas {
				if delta <= 0 || delta > remaining || (i == len(tc.WriteDeltas)-1 && delta != remaining) {
					return contract, fmt.Errorf("invalid completion delta %q", tc.Name)
				}
				remaining -= delta
				socket = append(socket, "write_progress")
				if remaining > 0 {
					delegate = append(delegate, fmt.Sprintf("partial_callback_socket_bytes_tag:%d:%d", delta, tc.Tag))
				}
			}
			socket = append([]string{"write_packet_enqueue"}, socket...)
			socket = append(socket, "complete_current_write", "end_current_write")
			delegate = append(delegate, fmt.Sprintf("complete_callback_socket_tag:%d", tc.Tag))
		case "no_progress_write":
			if tc.DataLength == 0 || len(tc.WriteDeltas) != 1 || tc.WriteDeltas[0] != 0 || tc.ReceiveTimeoutEnabled == nil || !*tc.ReceiveTimeoutEnabled {
				return contract, fmt.Errorf("invalid no-progress write %q", tc.Name)
			}
			submit = []string{"writeData_dispatches_internal_write_queue", "out_timeout_enable_after_writeData_returns", "receive_header_timeout_enable_after_writeData_returns"}
			socket = []string{"write_packet_enqueue", "write_attempt_no_progress"}
		case "complete_write":
			if tc.DataLength == 0 || len(tc.WriteDeltas) != 1 || tc.WriteDeltas[0] != tc.DataLength || tc.ReceiveTimeoutEnabled == nil {
				return contract, fmt.Errorf("invalid complete write %q", tc.Name)
			}
			submit = []string{"writeData_dispatches_internal_write_queue", "out_timeout_enable_after_writeData_returns"}
			if *tc.ReceiveTimeoutEnabled {
				submit = append(submit, "receive_header_timeout_enable_after_writeData_returns")
			}
			socket = []string{"write_packet_enqueue", "write_progress", "complete_current_write", "end_current_write"}
			delegate = []string{fmt.Sprintf("complete_callback_socket_tag:%d", tc.Tag)}
		default:
			return contract, fmt.Errorf("unknown case kind %q", tc.Kind)
		}
		if !equalStrings(tc.ExpectedSubmit, submit) || !equalStrings(tc.ExpectedSocket, socket) || !equalStrings(tc.ExpectedDelegate, delegate) {
			return contract, fmt.Errorf("event order mismatch %q", tc.Name)
		}
	}
	return contract, nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCocoaAsyncWriteContractSchema(t *testing.T) {
	if _, err := loadCocoaAsyncWriteContract(filepath.Join("testdata", "reconnect", "rc-q5-cocoa-async-write.json")); err != nil {
		t.Fatal(err)
	}
}
