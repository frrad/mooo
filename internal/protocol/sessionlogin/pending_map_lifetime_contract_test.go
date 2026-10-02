package sessionlogin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type pendingMapContract struct {
	Status string           `json:"status"`
	Cases  []pendingMapCase `json:"cases"`
}
type pendingMapCase struct {
	Name                  string   `json:"name"`
	Kind                  string   `json:"kind"`
	Evidence              []string `json:"evidence"`
	CompletionPresent     *bool    `json:"completion_present"`
	DefaultHandlerPresent *bool    `json:"default_handler_present"`
	MapNonEmpty           *bool    `json:"map_nonempty"`
	UniqueIDMatch         *bool    `json:"unique_id_match"`
	Expect                []string `json:"expect"`
	RemainingGaps         []string `json:"remaining_gaps"`
}

var pendingMapKinds = map[string]bool{"response-completion": true, "packet-id-timeout": true}
var pendingMapEffects = map[string]bool{
	"header_unique_id_lookup":           true,
	"completion_map_lookup":             true,
	"remove_completion_before_callback": true,
	"invoke_packet_nil_error":           true,
	"route_default_handler":             true,
	"no_default_handler_effect":         true,
	"packet_id_map_lookup":              true,
	"compare_incoming_unique_id":        true,
	"derive_timeout_tag":                true,
	"disable_matching_timeout":          true,
	"no_packet_id_map_removal_observed": true,
	"no_timeout_action":                 true,
}

func loadPendingMapContract(path string) (pendingMapContract, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return pendingMapContract{}, err
	}
	var v pendingMapContract
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, err
	}
	if v.Status != "reviewed-static-unexecuted-runtime" || len(v.Cases) != 4 {
		return v, fmt.Errorf("header/cases")
	}
	seen := map[string]bool{}
	for _, c := range v.Cases {
		if c.Name == "" || seen[c.Name] || !pendingMapKinds[c.Kind] || len(c.Evidence) == 0 || len(c.Expect) == 0 {
			return v, fmt.Errorf("case=%q", c.Name)
		}
		seen[c.Name] = true
		for _, id := range c.Evidence {
			if id != "RC-BIN-020" {
				return v, fmt.Errorf("evidence=%q", id)
			}
		}
		for _, e := range c.Expect {
			if !pendingMapEffects[e] {
				return v, fmt.Errorf("effect=%q", e)
			}
		}
		if c.Kind == "response-completion" && (c.CompletionPresent == nil || c.DefaultHandlerPresent == nil || c.MapNonEmpty == nil) {
			return v, fmt.Errorf("response inputs")
		}
		if c.Kind == "packet-id-timeout" && (c.MapNonEmpty == nil || c.UniqueIDMatch == nil) {
			return v, fmt.Errorf("timeout inputs")
		}
	}
	return v, nil
}

func TestPendingMapContractSchema(t *testing.T) {
	v, err := loadPendingMapContract(filepath.Join("testdata", "reconnect", "rc-q5-pending-map.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Cases[0].Expect; len(got) < 3 || got[1] != "completion_map_lookup" || got[2] != "remove_completion_before_callback" {
		t.Fatalf("match order=%v", got)
	}
	if got := v.Cases[2].Expect; len(got) < 4 || got[0] != "packet_id_map_lookup" || got[2] != "derive_timeout_tag" {
		t.Fatalf("timeout order=%v", got)
	}
}
