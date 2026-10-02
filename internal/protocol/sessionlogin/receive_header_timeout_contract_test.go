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
	TimeoutSeconds          *float64 `json:"timeout_seconds"`
	ExecutionTimeoutSeconds *float64 `json:"execution_timeout_seconds"`
	HandlerPresent          *bool    `json:"handler_present"`
	OldStatus               *int8    `json:"old_status"`
	NewStatus               *int8    `json:"new_status"`
	Tag                     *int64   `json:"tag"`
	Enable                  *bool    `json:"enable"`
	EnableByte              *uint8   `json:"enable_byte"`
	PacketID                *uint32  `json:"packet_id"`
	ExpectedTag             *int64   `json:"expected_tag"`
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

var knownReceiveHeaderTimeoutEffect = map[string]bool{"read_timeout": true, "check_tag_nonnegative": true, "queue_main": true, "no_enqueue": true, "reread_timeout": true, "perform_selector_after_delay_0": true, "perform_selector_after_delay_7": true, "owner_target": true, "fire_selector": true, "wrapped_tag": true, "cancel_previous_perform": true, "write_status_byte_zero": true, "set_status_zero": true, "invoke_status_handler_old_new_error": true, "no_status_handler": true, "cancel_owner_delayed_work": true, "enumerate_pending": true, "completion_nil": true, "error_domain_locoagent": true, "error_code_minus_one": true, "error_userinfo_nil": true, "lookup_unsigned_packet_id": true, "compare_stored_string_to_incoming_unique_id": true, "derive_request_tag": true, "derive_request_tag_identity": true, "disable_timeout": true, "comparison_false": true, "no_timeout_action": true, "register_completion_by_unique_id": true, "store_unique_id_by_packet_id": true, "send_packet": true, "arm_timeout": true, "forward_error": true, "no_timeout_arm": true, "no_callback": true}

func validateReceiveHeaderTimeoutContract(v receiveHeaderTimeoutContract) error {
	if v.Status != "reviewed-static-unexecuted-runtime" {
		return fmt.Errorf("status=%q", v.Status)
	}
	if len(v.Questions) != 1 || v.Questions[0] != "RC-Q5" {
		return fmt.Errorf("questions=%v", v.Questions)
	}
	if len(v.Cases) != 20 {
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
		for _, id := range c.Evidence {
			if id != "RC-BIN-010" && id != "RC-BIN-011" && id != "RC-BIN-012" && id != "RC-BIN-013" {
				return fmt.Errorf("unknown evidence %q", id)
			}
		}
		for _, effect := range c.Expect {
			if !knownReceiveHeaderTimeoutEffect[effect] {
				return fmt.Errorf("unknown effect %q", effect)
			}
		}
		if c.Enable != nil && c.EnableByte != nil {
			return fmt.Errorf("ambiguous enable inputs: %q", c.Name)
		}
		switch c.Kind {
		case "timeout-admission":
			if c.TimeoutSeconds == nil || c.Tag == nil || c.Enable == nil || c.EnableByte != nil {
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
			if c.Kind == "timeout-disable" && c.Enable != nil && *c.Enable {
				return fmt.Errorf("disable enable=true: %q", c.Name)
			}
			if c.Kind == "timeout-disable" && c.EnableByte != nil && *c.EnableByte == 1 {
				return fmt.Errorf("disable enable-byte=1: %q", c.Name)
			}
		case "disconnect-fanout":
			if c.HandlerPresent == nil || c.OldStatus == nil || c.NewStatus == nil {
				return fmt.Errorf("handler inputs missing: %q", c.Name)
			}
		case "completion-disarm":
			if c.PacketID == nil || c.IncomingUniqueID == nil {
				return fmt.Errorf("disarm inputs missing: %q", c.Name)
			}
			identity := false
			for _, effect := range c.Expect {
				if effect == "derive_request_tag_identity" { identity = true }
			}
			if identity && c.ExpectedTag == nil { return fmt.Errorf("identity expected tag missing: %q", c.Name) }
			if identity && uint64(*c.ExpectedTag) != uint64(*c.PacketID) { return fmt.Errorf("identity tag=%d packet=%d: %q", *c.ExpectedTag, *c.PacketID, c.Name) }
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
	fanout := v.Cases[9]
	if got := fanout.Expect; len(got) != 9 || got[6] != "error_domain_locoagent" || got[7] != "error_code_minus_one" || got[8] != "error_userinfo_nil" {
		t.Fatalf("fanout=%v", got)
	}
	matching := v.Cases[12]
	if matching.StoredUniqueID == nil || *matching.StoredUniqueID != *matching.IncomingUniqueID {
		t.Fatalf("matching inputs=%v", matching)
	}
	if got := v.Cases[16].Expect; len(got) != 4 || got[0] != "register_completion_by_unique_id" || got[2] != "send_packet" || got[3] != "arm_timeout" {
		t.Fatalf("producer order=%v", got)
	}
}
