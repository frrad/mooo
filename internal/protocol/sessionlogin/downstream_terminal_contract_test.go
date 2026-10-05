package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type downstreamTerminalContract struct {
	Status string `json:"status"`
	Cases  []struct {
		Name            string   `json:"name"`
		ExpectedEffects []string `json:"expected_effects"`
	} `json:"cases"`
}

func TestDownstreamTerminalLifecycleFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-downstream-terminal-lifecycle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture downstreamTerminalContract
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Status != "reviewed-static-unexecuted-runtime" || len(fixture.Cases) != 4 {
		t.Fatalf("fixture header = %#v", fixture)
	}
	seen := map[string]bool{}
	for _, tc := range fixture.Cases {
		if tc.Name == "" || seen[tc.Name] || len(tc.ExpectedEffects) == 0 {
			t.Fatalf("invalid fixture case %#v", tc)
		}
		seen[tc.Name] = true
	}
	effects := fixture.Cases[0].ExpectedEffects
	if len(effects) < 2 || effects[len(effects)-2] != "disconnect_ticket_agent" || effects[len(effects)-1] != "disconnect_carriage_agent" {
		t.Fatalf("changesvr disconnect order = %v", effects)
	}
}
