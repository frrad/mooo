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

type carriageTimeoutContract struct {
	Status string                `json:"status"`
	Cases  []carriageTimeoutCase `json:"cases"`
}

type carriageTimeoutCase struct {
	Name             string    `json:"name"`
	ConfigSeconds    []float64 `json:"config_seconds"`
	ExpectedCtorArgs []float64 `json:"expected_constructor_seconds"`
	ExpectedEffects  []string  `json:"expected_effects"`
}

func loadCarriageTimeoutContract(path string) (carriageTimeoutContract, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return carriageTimeoutContract{}, err
	}
	var contract carriageTimeoutContract
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&contract); err != nil {
		return contract, err
	}
	if contract.Status != "reviewed-static-unexecuted-runtime" || len(contract.Cases) != 3 {
		return contract, fmt.Errorf("invalid status=%q cases=%d", contract.Status, len(contract.Cases))
	}
	seen := map[string]bool{}
	wantEffects := []string{
		"read_connect_timeout_seconds",
		"read_receive_header_timeout_seconds",
		"read_in_segment_timeout_seconds",
		"read_out_segment_timeout_seconds",
		"construct_loco_agent_with_same_timeout_order",
		"construct_loco_agent_server_type_3",
		"construct_loco_agent_secure_layer_type_1",
		"disable_agent_fallback",
		"install_carriage_agent",
		"install_status_handler",
		"manager_set_status_0x15",
		"connect_agent",
	}
	for _, tc := range contract.Cases {
		if tc.Name == "" || seen[tc.Name] || len(tc.ConfigSeconds) != 4 || len(tc.ExpectedCtorArgs) != 4 || len(tc.ExpectedEffects) == 0 {
			return contract, fmt.Errorf("invalid case %q", tc.Name)
		}
		seen[tc.Name] = true
		if !reflect.DeepEqual(tc.ConfigSeconds, tc.ExpectedCtorArgs) {
			return contract, fmt.Errorf("constructor forwarding mismatch %q", tc.Name)
		}
		if !reflect.DeepEqual(tc.ExpectedEffects, wantEffects) {
			return contract, fmt.Errorf("effect order mismatch %q", tc.Name)
		}
	}
	return contract, nil
}

func TestCarriageTimeoutProvenanceSchema(t *testing.T) {
	if _, err := loadCarriageTimeoutContract(filepath.Join("testdata", "reconnect", "rc-q5-carriage-timeout-provenance.json")); err != nil {
		t.Fatal(err)
	}
}
