package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type carriageAgentFixture struct {
	Status string              `json:"status"`
	Cases  []carriageAgentCase `json:"cases"`
}
type carriageAgentCase struct {
	Name                    string   `json:"name"`
	Operation               string   `json:"operation"`
	CurrentAgentIdentity    string   `json:"current_agent_identity"`
	NewAgentIdentity        string   `json:"new_agent_identity"`
	CallbackArgIdentity     string   `json:"callback_arg_identity"`
	ExpectedCurrentIdentity string   `json:"expected_current_identity"`
	ExpectedEffects         []string `json:"expected_effects"`
}

func expectedCarriageAgent(c carriageAgentCase) []string {
	switch c.Operation {
	case "receipt_send":
		return []string{"read_current_carriage_agent", "dynamic_send_push_receipt"}
	case "connect_set":
		return []string{"construct_carriage_agent", "disable_fallback", "set_carriage_agent", "install_status_handler", "set_manager_status", "connect_agent"}
	case "factory_selection":
		return []string{"factory_selection_unproven"}
	default:
		return nil
	}
}
func TestPushReceiptCarriageAgentFixture(t *testing.T) {
	b, e := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-push-receipt-carriage-agent.json"))
	if e != nil {
		t.Fatal(e)
	}
	var f carriageAgentFixture
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(&f); e != nil {
		t.Fatal(e)
	}
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 3 {
		t.Fatalf("header %#v", f)
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if seen[c.Name] || c.Name == "" {
			t.Fatal("duplicate name")
		}
		seen[c.Name] = true
		if c.Operation != "receipt_send" && c.Operation != "connect_set" && c.Operation != "factory_selection" {
			t.Errorf("%s operation=%q", c.Name, c.Operation)
		}
		if g, w := c.ExpectedEffects, expectedCarriageAgent(c); !reflect.DeepEqual(g, w) {
			t.Errorf("%s=%v want %v", c.Name, g, w)
		}
		switch c.Operation {
		case "receipt_send":
			if c.CurrentAgentIdentity == "" || c.CallbackArgIdentity != c.CurrentAgentIdentity || c.ExpectedCurrentIdentity != c.CurrentAgentIdentity {
				t.Errorf("%s receipt identity handoff=%q callback=%q expected=%q", c.Name, c.CurrentAgentIdentity, c.CallbackArgIdentity, c.ExpectedCurrentIdentity)
			}
		case "connect_set":
			if c.NewAgentIdentity == "" || c.CallbackArgIdentity != c.NewAgentIdentity || c.ExpectedCurrentIdentity != c.NewAgentIdentity {
				t.Errorf("%s connect identity current=%q new=%q callback=%q expected=%q", c.Name, c.CurrentAgentIdentity, c.NewAgentIdentity, c.CallbackArgIdentity, c.ExpectedCurrentIdentity)
			}
		case "factory_selection":
			if c.ExpectedCurrentIdentity != "" || c.NewAgentIdentity != "" || c.CallbackArgIdentity != "" {
				t.Errorf("%s unproven factory must not invent identities", c.Name)
			}
		}
	}
}
