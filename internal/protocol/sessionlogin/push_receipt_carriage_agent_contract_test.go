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
	Name                 string   `json:"name"`
	AgentPropertyPresent bool     `json:"agent_property_present"`
	SelectionProven      bool     `json:"selection_proven"`
	ExpectedEffects      []string `json:"expected_effects"`
}

func expectedCarriageAgent(c carriageAgentCase) []string {
	if c.AgentPropertyPresent {
		if c.Name[0] == 'r' {
			return []string{"read_carriage_agent", "dynamic_send_push_receipt"}
		}
		return []string{"set_carriage_agent"}
	}
	return []string{"transport_factory_selection_gap"}
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
		if g, w := c.ExpectedEffects, expectedCarriageAgent(c); !reflect.DeepEqual(g, w) {
			t.Errorf("%s=%v want %v", c.Name, g, w)
		}
	}
}
