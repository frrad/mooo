package sessionlogin

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	Expected      []string `json:"expected"`
}

var secureEffects = map[string]bool{"header_read_length_4": true, "header_read_length_22": true, "header_timeout_minus_one": true, "header_tag_zero": true, "header_prefix_zero": true, "header_prefix_nonzero": true, "read_header_again": true, "read_body_length": true, "supply_raw_data": true, "route_body": true, "decrypt_accumulated": true}

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
	if v.Status != "reviewed-static-unexecuted-runtime" || v.Question != "RC-Q5" || len(v.Cases) != 6 {
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
	}
	_ = fmt.Sprintf("%v", v)
}
