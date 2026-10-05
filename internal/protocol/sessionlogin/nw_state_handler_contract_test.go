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
	AllowFallback                bool     `json:"allow_fallback"`
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
		return nwStateResult{Effects: effects, CancelID: c.CurrentConnectionID}
	}
	if !c.OwnerPresent {
		return nwStateResult{Effects: effects}
	}
	switch c.State {
	case "waiting", "failed":
		effects = append(effects, "extract_state_error", "handle_connect_failure")
		if c.AllowFallback {
			effects = append(effects, "allow_fallback_input")
		}
	case "setup", "preparing":
		effects = append(effects, "log_state")
	case "ready":
		effects = append(effects, "log_tls_version", "ready_followup")
	case "cancelled":
		effects = append(effects, "cancel_work_item", "construct_locoagent_error", "dispatch_main_queue")
	default:
		effects = append(effects, "log_unknown_state")
	}
	return nwStateResult{Effects: effects, CancelID: c.CurrentConnectionID}
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
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 10 {
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
