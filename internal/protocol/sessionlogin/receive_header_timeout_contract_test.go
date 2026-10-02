package sessionlogin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The contract fixture is intentionally schema-only: runtime queue/failure
// integration remains unexecuted until the independent owner is implemented.
type receiveHeaderTimeoutContract struct {
	Status    string                     `json:"status"`
	Questions []string                   `json:"questions"`
	Cases     []receiveHeaderTimeoutCase `json:"cases"`
}

type receiveHeaderTimeoutCase struct {
	Name                    string   `json:"name"`
	Kind                    string   `json:"kind"`
	Evidence                []string `json:"evidence"`
	TimeoutSeconds          *int64   `json:"timeout_seconds"`
	ExecutionTimeoutSeconds *int64   `json:"execution_timeout_seconds"`
	Tag                     *int64   `json:"tag"`
	Enable                  *bool    `json:"enable"`
	EnableByte              *uint8   `json:"enable_byte"`
	PacketID                *uint32  `json:"packet_id"`
	StoredUniqueID          *string  `json:"stored_unique_id"`
	IncomingUniqueID        *string  `json:"incoming_unique_id"`
	ProducerStatus          *uint8   `json:"producer_status"`
	CompletionPresent       *bool    `json:"completion_present"`
	Expect                  []string `json:"expect"`
	RemainingGaps           []string `json:"remaining_gaps"`
}

func loadReceiveHeaderTimeoutContract(path string) (receiveHeaderTimeoutContract, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return receiveHeaderTimeoutContract{}, err
	}
	var v receiveHeaderTimeoutContract
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, err
	}
	return v, validateReceiveHeaderTimeoutContract(v)
}

func validateReceiveHeaderTimeoutContract(v receiveHeaderTimeoutContract) error {
	if v.Status != "reviewed-static-unexecuted-runtime" {
		return fmt.Errorf("status=%q", v.Status)
	}
	if len(v.Questions) != 1 || v.Questions[0] != "RC-Q5" {
		return fmt.Errorf("questions=%v", v.Questions)
	}
	if len(v.Cases) != 16 {
		return fmt.Errorf("cases=%d", len(v.Cases))
	}
	known := map[string]bool{"timeout-admission": true, "timeout-enable": true, "timeout-disable": true, "disconnect-fanout": true, "completion-disarm": true, "packet-production": true}
	seen := map[string]bool{}
	for _, c := range v.Cases {
		if c.Name == "" || seen[c.Name] {
			return fmt.Errorf("duplicate/empty case %q", c.Name)
		}
		seen[c.Name] = true
		if !known[c.Kind] || len(c.Evidence) == 0 || len(c.Expect) == 0 {
			return fmt.Errorf("invalid case %q", c.Name)
		}
		switch c.Kind {
		case "timeout-admission":
			if c.TimeoutSeconds == nil || c.Tag == nil || c.Enable == nil {
				return fmt.Errorf("admission inputs missing: %q", c.Name)
			}
		case "timeout-enable", "timeout-disable":
			if c.TimeoutSeconds == nil || c.ExecutionTimeoutSeconds == nil || c.Tag == nil {
				return fmt.Errorf("timeout inputs missing: %q", c.Name)
			}
			if c.Kind == "timeout-enable" && (c.Enable == nil || !*c.Enable) {
				return fmt.Errorf("enable=true input missing: %q", c.Name)
			}
			if c.Kind == "timeout-disable" && c.Enable == nil && c.EnableByte == nil {
				return fmt.Errorf("disable byte input missing: %q", c.Name)
			}
		case "completion-disarm":
			if c.PacketID == nil || c.IncomingUniqueID == nil {
				return fmt.Errorf("disarm inputs missing: %q", c.Name)
			}
		case "packet-production":
			if c.ProducerStatus == nil || c.CompletionPresent == nil {
				return fmt.Errorf("producer inputs missing: %q", c.Name)
			}
		}
	}
	return nil
}

func TestReceiveHeaderTimeoutContractSchema(t *testing.T) {
	v, err := loadReceiveHeaderTimeoutContract(filepath.Join("testdata", "reconnect", "rc-q5-timeout-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	fanout := v.Cases[8]
	if got := fanout.Expect; len(got) != 7 || got[4] != "error_domain_locoagent" || got[5] != "error_code_minus_one" || got[6] != "error_userinfo_nil" {
		t.Fatalf("fanout=%v", got)
	}
	matching := v.Cases[9]
	if matching.StoredUniqueID == nil || *matching.StoredUniqueID != *matching.IncomingUniqueID {
		t.Fatalf("matching inputs=%v", matching)
	}
	if got := v.Cases[12].Expect; len(got) != 4 || got[0] != "register_completion_by_unique_id" || got[2] != "send_packet" || got[3] != "arm_timeout" {
		t.Fatalf("producer order=%v", got)
	}
}
