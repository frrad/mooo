package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type nwStateFixture struct {
	Status string        `json:"status"`
	Cases  []nwStateCase `json:"cases"`
}
type nwStateCase struct {
	Name                         string   `json:"name"`
	Operation                    string   `json:"operation"`
	State                        string   `json:"state"`
	OwnerPresent                 bool     `json:"owner_present"`
	CurrentConnectionPresent     bool     `json:"current_connection_present"`
	CurrentConnectionID          string   `json:"current_connection_id"`
	ReplacementConnectionPresent bool     `json:"replacement_connection_present"`
	ReceiveWorkItemPresent       bool     `json:"receive_work_item_present"`
	PathPresent                  bool     `json:"path_present"`
	PathStatus                   string   `json:"path_status"`
	ImmediateFailure             bool     `json:"immediate_failure"`
	HelperFallbackPredicate      bool     `json:"helper_fallback_predicate"`
	OwnerFlagA                   bool     `json:"owner_flag_a"`
	OwnerFlagB                   bool     `json:"owner_flag_b"`
	ExpectedCancelID             string   `json:"expected_cancel_id"`
	Expected                     []string `json:"expected_effects"`
	PendingGap                   bool     `json:"pending_map_gap"`
}

type nwStateResult struct {
	Effects  []string
	CancelID string
}

func projectNWState(c nwStateCase) nwStateResult {
	effects := make([]string, 0)
	if c.Operation == "setup" {
		if c.CurrentConnectionPresent {
			effects = append(effects, "cancel_previous_current", "release_previous_current")
		}
		if c.ReplacementConnectionPresent {
			effects = append(effects, "store_replacement", "start_on_queue")
		}
		return nwStateResult{Effects: effects}
	}
	if !c.OwnerPresent {
		return nwStateResult{Effects: effects}
	}
	switch c.State {
	case "waiting":
		// The dispatcher does not call handleConnectFailure for every waiting
		// callback. It first requires clear owner flags, a path, and one of the
		// two observed path statuses, then evaluates the fallback/immediate
		// predicates.
		if !c.OwnerFlagA && !c.OwnerFlagB && c.PathPresent &&
			(c.PathStatus == "satisfied" || c.PathStatus == "requires_connection") &&
			(c.HelperFallbackPredicate || c.ImmediateFailure) {
			effects = append(effects, "invoke_failure_helper")
		} else {
			effects = append(effects, "log_state")
		}
	case "failed":
		// failed always invokes the helper with allowFallback=true; the helper
		// owns cleanup and the fallback/error split.
		effects = append(effects, "invoke_failure_helper")
		if c.ReceiveWorkItemPresent {
			effects = append(effects, "cancel_receive_work_item")
		}
		if c.CurrentConnectionPresent {
			effects = append(effects, "clear_state_handler", "cancel_current_connection")
		}
		if c.ImmediateFailure || !c.HelperFallbackPredicate || c.OwnerFlagA || c.OwnerFlagB {
			effects = append(effects, "convert_nw_error", "construct_locoagent_error", "dispatch_main_queue")
		} else {
			effects = append(effects, "fallback_to_v2sl")
		}
	case "setup", "preparing":
		effects = append(effects, "log_state")
	case "ready":
		effects = append(effects, "log_tls_version", "ready_followup")
	case "cancelled":
		if c.ReceiveWorkItemPresent {
			effects = append(effects, "cancel_receive_work_item")
		}
		effects = append(effects, "construct_locoagent_error", "dispatch_main_queue")
	default:
		effects = append(effects, "log_unknown_state")
	}
	return nwStateResult{Effects: effects}
}

func TestNWStateHandlerFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-nw-state-handler.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f nwStateFixture
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 17 {
		t.Fatalf("header %#v", f)
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty case %q", c.Name)
		}
		seen[c.Name] = true
		if !c.PendingGap {
			t.Errorf("%s must preserve pending map gap", c.Name)
		}
		got := projectNWState(c)
		if !reflect.DeepEqual(got.Effects, c.Expected) || got.CancelID != c.ExpectedCancelID {
			t.Errorf("%s result=%+v want effects=%v cancel=%q", c.Name, got, c.Expected, c.ExpectedCancelID)
		}
	}
}
