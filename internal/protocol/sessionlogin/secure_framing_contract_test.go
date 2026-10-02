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
	Name                   string   `json:"name"`
	CryptoPresent          bool     `json:"crypto_present"`
	ReadTag                int      `json:"read_tag"`
	PrefixValue            *uint32  `json:"prefix_value"`
	Accumulated            int      `json:"accumulated_bytes"`
	BodyLength             int      `json:"body_length"`
	BodyLengths            []int    `json:"body_lengths"`
	HeaderPresent          bool     `json:"current_header_present"`
	HeaderCapable          bool     `json:"delegate_supports_header"`
	PacketCapable          bool     `json:"delegate_supports_packet"`
	IdentityMatch          bool     `json:"timeout_identity_matches"`
	DelegateIsAgent        bool     `json:"delegate_is_agent"`
	ExpectedRemainingBytes *int     `json:"expected_remaining_bytes"`
	ExpectedHeaderPresent  *bool    `json:"expected_current_header_present"`
	Expected               []string `json:"expected"`
}

var secureEffects = map[string]bool{"header_read_length_4": true, "header_read_length_22": true, "header_timeout_minus_one": true, "header_tag_zero": true, "header_prefix_zero": true, "header_prefix_nonzero": true, "read_header_again": true, "read_body_length": true, "supply_raw_data": true, "route_body": true, "decrypt_accumulated": true, "header_callback_if_supported": true, "complete_packet_callback_after_body": true, "conditional_timeout_disarm_after_header": true, "producer_loop_next_frame": true, "decode_packet_id_u32": true, "decode_status_u16": true, "decode_method_utf8_11": true, "decode_body_type_u8": true, "decode_body_length_u32": true, "buffer_retained_below_minimum": true}

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
	if v.Status != "reviewed-static-unexecuted-runtime" || v.Question != "RC-Q5" || len(v.Cases) != 13 {
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
		headerCount, packetCount := 0, 0
		for _, x := range c.Expected {
			switch x {
			case "header_callback_if_supported":
				headerCount++
			case "complete_packet_callback_after_body":
				packetCount++
				if !c.PacketCapable || c.Accumulated < 22+c.BodyLength {
					t.Fatalf("complete callback preconditions missing: %s", c.Name)
				}
			case "conditional_timeout_disarm_after_header":
				if !c.IdentityMatch {
					t.Fatalf("disarm without identity match: %s", c.Name)
				}
			}
		}
		if headerCount > 0 && (!c.HeaderCapable || c.Accumulated < 22) {
			t.Fatalf("header callback preconditions missing: %s", c.Name)
		}
		if c.ExpectedRemainingBytes != nil && *c.ExpectedRemainingBytes < 0 {
			t.Fatalf("negative remaining bytes: %s", c.Name)
		}
		if packetCount > headerCount && c.HeaderCapable {
			t.Fatalf("packet callbacks exceed header callbacks: %s", c.Name)
		}
		if len(c.BodyLengths) > 0 {
			if packetCount != len(c.BodyLengths) || len(c.BodyLengths) != 2 {
				t.Fatalf("body frame cardinality mismatch: %s", c.Name)
			}
			for _, n := range c.BodyLengths {
				if n < 0 {
					t.Fatalf("negative body length: %s", c.Name)
				}
			}
		}
		if c.ExpectedRemainingBytes != nil && *c.ExpectedRemainingBytes < 0 {
			t.Fatalf("negative remaining bytes: %s", c.Name)
		}
		if c.ExpectedHeaderPresent != nil && *c.ExpectedHeaderPresent != c.HeaderPresent && c.Name != "producer loops over two complete frames" {
			t.Fatalf("header state mismatch: %s", c.Name)
		}
		if c.Name == "producer header disarm precedes complete packet" {
			want := []string{"header_callback_if_supported", "conditional_timeout_disarm_after_header", "complete_packet_callback_after_body"}
			found := false
			for start := 0; start+len(want) <= len(c.Expected); start++ {
				ok := true
				for i, effect := range want {
					if c.Expected[start+i] != effect {
						ok = false
						break
					}
				}
				if ok {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("callback order mismatch: %s", c.Name)
			}
		}
	}
}
