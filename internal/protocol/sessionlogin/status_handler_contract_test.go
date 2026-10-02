package sessionlogin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type statusHandlerContract struct {
	Status    string              `json:"status"`
	Questions []string            `json:"questions"`
	Cases     []statusHandlerCase `json:"cases"`
}
type statusHandlerCase struct {
	Name           string   `json:"name"`
	Kind           string   `json:"kind"`
	Evidence       []string `json:"evidence"`
	HandlerPresent *bool    `json:"handler_present"`
	OldStatus      *int8    `json:"old_status"`
	NewStatus      *int8    `json:"new_status"`
	AgentCurrent   *bool    `json:"agent_current"`
	LatchInitial   *bool    `json:"latch_initial"`
	PendingCount   *int     `json:"pending_count"`
	Expect         []string `json:"expect"`
	RemainingGaps  []string `json:"remaining_gaps"`
}

var statusHandlerKinds = map[string]bool{"status-setter": true, "manager-status-zero": true, "manager-status-three": true, "manager-status-gate": true, "disconnect-fanout-order": true, "owner-capture": true, "pending-response": true}
var statusHandlerEffects = map[string]bool{"write_status_byte": true, "invoke_handler_agent_old_new_error": true, "no_handler_callback": true, "compare_current_agent": true, "clear_agent_slot": true, "write_status_0x16": true, "write_status_0x1a": true, "callback_false": true, "no_false_callback": true, "queue_ping_cancel": true, "clear_status_handler": true, "write_status_0x17": true, "set_latch": true, "callback_true": true, "no_latch_write": true, "no_true_callback": true, "no_effects": true, "cancel_owner_delayed_work": true, "enumerate_pending": true, "fanout_each_nil_locoagent_error": true, "weak_send_capture": true, "strong_timeout_capture": true, "no_identity_equality_proof": true, "lookup_completion_by_unique_id": true, "remove_completion_before_callback": true, "invoke_completion_packet_nil_error": true, "route_unmatched_to_default_handler": true}

func loadStatusHandlerContract(path string) (statusHandlerContract, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return statusHandlerContract{}, err
	}
	var v statusHandlerContract
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, err
	}
	return v, validateStatusHandlerContract(v)
}
func validateStatusHandlerContract(v statusHandlerContract) error {
	if v.Status != "reviewed-static-unexecuted-runtime" || len(v.Questions) != 1 || v.Questions[0] != "RC-Q5" {
		return fmt.Errorf("header mismatch")
	}
	if len(v.Cases) != 11 {
		return fmt.Errorf("cases=%d", len(v.Cases))
	}
	seen := map[string]bool{}
	for _, c := range v.Cases {
		if c.Name == "" || seen[c.Name] || !statusHandlerKinds[c.Kind] || len(c.Evidence) == 0 || len(c.Expect) == 0 {
			return fmt.Errorf("invalid case %q", c.Name)
		}
		seen[c.Name] = true
		for _, id := range c.Evidence {
			if id < "RC-BIN-014" || id > "RC-BIN-018" {
				return fmt.Errorf("evidence=%q", id)
			}
		}
		for _, e := range c.Expect {
			if !statusHandlerEffects[e] {
				return fmt.Errorf("effect=%q", e)
			}
		}
		switch c.Kind {
		case "status-setter":
			if c.HandlerPresent == nil || c.OldStatus == nil || c.NewStatus == nil {
				return fmt.Errorf("setter inputs")
			}
		case "manager-status-zero", "manager-status-three":
			if c.AgentCurrent == nil || c.LatchInitial == nil {
				return fmt.Errorf("status inputs")
			}
		case "manager-status-gate":
			if c.AgentCurrent == nil {
				return fmt.Errorf("gate inputs")
			}
		case "disconnect-fanout-order":
			if c.HandlerPresent == nil || c.PendingCount == nil {
				return fmt.Errorf("fanout inputs")
			}
		case "owner-capture":
		case "pending-response":
			if c.PendingCount == nil || c.HandlerPresent == nil {
				return fmt.Errorf("pending response inputs")
			}
		}
	}
	return nil
}
func TestStatusHandlerContractSchema(t *testing.T) {
	v, err := loadStatusHandlerContract(filepath.Join("testdata", "reconnect", "rc-q5-status-handler.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Cases[7].Expect; len(got) != 5 || got[1] != "invoke_handler_agent_old_new_error" || got[3] != "enumerate_pending" {
		t.Fatalf("order=%v", got)
	}
	if got := v.Cases[8].RemainingGaps; len(got) != 1 || got[0] != "creator_identity_dataflow" {
		t.Fatalf("gaps=%v", got)
	}
}
