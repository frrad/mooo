package sessionlogin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type agentStatusGateContract struct {
	Status string                `json:"status"`
	Cases  []agentStatusGateCase `json:"cases"`
}

type agentStatusGateCase struct {
	Name             string   `json:"name"`
	Kind             string   `json:"kind"`
	OwnerClass       string   `json:"owner_class"`
	AdmissionStatus  uint8    `json:"admission_status"`
	SecureLayerType  *uint8   `json:"secure_layer_type"`
	CryptoPresent    bool     `json:"crypto_present"`
	ExecutionStatus  *uint8   `json:"execution_status"`
	HandshakeTimeout *float64 `json:"handshake_timeout_seconds"`
	HandshakeTag     *int     `json:"handshake_tag"`
	ExpectedEffects  []string `json:"expected_effects"`
	RemainingGaps    []string `json:"remaining_gaps"`
}

var knownAgentStatusGateEffect = map[string]bool{
	"set_agent_status_2":                       true,
	"start_tls":                                true,
	"create_v2sl_crypto":                       true,
	"write_v2sl_handshake":                     true,
	"write_v2sl_handshake_timeout_minus1_tag0": true,
	"set_agent_status_3":                       true,
	"read_header":                              true,
	"status_gate_reads_execution_status":       true,
	"status_gate_allows_packet_allocation":     true,
	"status_gate_allows_send":                  true,
	"status_gate_allows_receive_timeout_arm":   true,
	"status_gate_rejects_packet_allocation":    true,
	"status_gate_rejects_send":                 true,
	"status_gate_rejects_receive_timeout_arm":  true,
}

func loadAgentStatusGateContract(path string) (agentStatusGateContract, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return agentStatusGateContract{}, err
	}
	var contract agentStatusGateContract
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&contract); err != nil {
		return contract, err
	}
	if contract.Status != "reviewed-static-unexecuted-runtime" || len(contract.Cases) != 9 {
		return contract, fmt.Errorf("invalid status=%q cases=%d", contract.Status, len(contract.Cases))
	}
	seen := map[string]bool{}
	for _, tc := range contract.Cases {
		if tc.Name == "" || seen[tc.Name] || tc.OwnerClass != "LocoAgent" || len(tc.ExpectedEffects) == 0 || len(tc.RemainingGaps) == 0 {
			return contract, fmt.Errorf("invalid case %q", tc.Name)
		}
		seen[tc.Name] = true
		for _, effect := range tc.ExpectedEffects {
			if !knownAgentStatusGateEffect[effect] {
				return contract, fmt.Errorf("unknown effect %q", effect)
			}
		}
		var want []string
		switch tc.Kind {
		case "connect":
			if tc.SecureLayerType == nil {
				return contract, fmt.Errorf("connect case missing secure layer type %q", tc.Name)
			}
			if tc.ExecutionStatus != nil || tc.AdmissionStatus != 3 {
				return contract, fmt.Errorf("invalid connect status inputs %q", tc.Name)
			}
			want = []string{"set_agent_status_2"}
			switch *tc.SecureLayerType {
			case 0:
				if tc.CryptoPresent {
					return contract, fmt.Errorf("crypto on plain connect %q", tc.Name)
				}
			case 1:
				if tc.CryptoPresent {
					return contract, fmt.Errorf("crypto on TLS connect %q", tc.Name)
				}
				want = append(want, "start_tls")
			case 2:
				if !tc.CryptoPresent {
					return contract, fmt.Errorf("missing crypto on V2SL connect %q", tc.Name)
				}
				if tc.HandshakeTimeout == nil || *tc.HandshakeTimeout != -1 || tc.HandshakeTag == nil || *tc.HandshakeTag != 0 {
					return contract, fmt.Errorf("invalid V2SL handshake inputs %q", tc.Name)
				}
				want = append(want, "create_v2sl_crypto", "write_v2sl_handshake", "write_v2sl_handshake_timeout_minus1_tag0")
			default:
				// The callback has no setup branch for values other than 1 and 2.
			}
			want = append(want, "set_agent_status_3", "read_header")
		case "produce":
			if tc.SecureLayerType != nil {
				return contract, fmt.Errorf("producer case has connect-only secure input %q", tc.Name)
			}
			want = []string{"status_gate_reads_execution_status"}
			executionStatus := tc.AdmissionStatus
			if tc.ExecutionStatus != nil {
				executionStatus = *tc.ExecutionStatus
			}
			if executionStatus == 3 {
				want = append(want, "status_gate_allows_packet_allocation", "status_gate_allows_send", "status_gate_allows_receive_timeout_arm")
			} else {
				want = append(want, "status_gate_rejects_packet_allocation", "status_gate_rejects_send", "status_gate_rejects_receive_timeout_arm")
			}
		default:
			return contract, fmt.Errorf("unknown case kind %q", tc.Kind)
		}
		if len(want) != len(tc.ExpectedEffects) {
			return contract, fmt.Errorf("effect count mismatch %q", tc.Name)
		}
		for i := range want {
			if tc.ExpectedEffects[i] != want[i] {
				return contract, fmt.Errorf("effect order mismatch %q: got=%v want=%v", tc.Name, tc.ExpectedEffects, want)
			}
		}
	}
	return contract, nil
}

func TestAgentStatusGateContractSchema(t *testing.T) {
	if _, err := loadAgentStatusGateContract(filepath.Join("testdata", "reconnect", "rc-q5-agent-status-gate.json")); err != nil {
		t.Fatal(err)
	}
}
