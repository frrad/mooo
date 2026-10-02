package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type secureFrameContract struct {
	Status   string            `json:"status"`
	Question string            `json:"question"`
	Cases    []secureFrameCase `json:"cases"`
}
type secureFrameCase struct {
	Name          string   `json:"name"`
	CryptoPresent bool     `json:"crypto_present"`
	ReadTag       int      `json:"read_tag"`
	PrefixValue   *uint32  `json:"prefix_value"`
	Accumulated   int      `json:"accumulated_bytes"`
	HeaderPresent bool     `json:"current_header_present"`
	HeaderCapable bool     `json:"delegate_supports_header"`
	BodyComplete  bool     `json:"body_complete"`
	IdentityMatch bool     `json:"timeout_identity_matches"`
	Expected      []string `json:"expected"`
}

var secureEffects = map[string]bool{"header_read_length_4": true, "header_read_length_22": true, "header_timeout_minus_one": true, "header_tag_zero": true, "header_prefix_zero": true, "header_prefix_nonzero": true, "read_header_again": true, "read_body_length": true, "supply_raw_data": true, "route_body": true, "decrypt_accumulated": true, "header_callback_if_supported": true, "complete_packet_callback_after_body": true, "header_before_complete_packet": true, "conditional_timeout_disarm_after_header": true}

func TestSecureFramingContractSchema(t *testing.T) {
	b, e := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-secure-framing.json"))
	if e != nil {
		t.Fatal(e)
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var v secureFrameContract
	if e = d.Decode(&v); e != nil {
		t.Fatal(e)
	}
	if v.Status != "reviewed-static-unexecuted-runtime" || v.Question != "RC-Q5" || len(v.Cases) != 8 {
		t.Fatalf("header=%+v", v)
	}
	seen := map[string]bool{}
	for _, c := range v.Cases {
		if c.Name == "" || seen[c.Name] || len(c.Expected) == 0 {
			t.Fatalf("case=%+v", c)
		}
		seen[c.Name] = true
		for _, x := range c.Expected {
			if !secureEffects[x] {
				t.Fatalf("effect=%q", x)
			}
		}
		if c.CryptoPresent && c.PrefixValue == nil && c.ReadTag == 0 {
			t.Fatalf("crypto header prefix missing: %s", c.Name)
		}
		if c.ReadTag < 0 {
			t.Fatalf("tag=%d", c.ReadTag)
		}
		hasHeader := false
		for _, x := range c.Expected {
			if x == "header_callback_if_supported" {
				hasHeader = true
			}
			if x == "complete_packet_callback_after_body" && !c.BodyComplete {
				t.Fatalf("complete callback without body: %s", c.Name)
			}
			if x == "conditional_timeout_disarm_after_header" && !c.IdentityMatch {
				t.Fatalf("disarm without identity match: %s", c.Name)
			}
		}
		if hasHeader && (!c.HeaderCapable || c.Accumulated < 22) {
			t.Fatalf("header callback preconditions missing: %s", c.Name)
		}
	}
}
