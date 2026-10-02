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
	Name                  string   `json:"name"`
	CryptoPresent         bool     `json:"crypto_present"`
	ReadTag               int      `json:"read_tag"`
	PrefixValue           *uint32  `json:"prefix_value"`
	Accumulated           int      `json:"accumulated_bytes"`
	BodyLength            int      `json:"body_length"`
	BodyLengths           []int    `json:"body_lengths"`
	HeaderPresent         bool     `json:"current_header_present"`
	HeaderCapable         bool     `json:"delegate_supports_header"`
	PacketCapable         bool     `json:"delegate_supports_packet"`
	IdentityMatch         bool     `json:"timeout_identity_matches"`
	DelegateIsAgent       bool     `json:"delegate_is_agent"`
	ExpectedBufferedBytes *int     `json:"expected_buffered_bytes"`
	ExpectedBytesNeeded   *int     `json:"expected_bytes_needed"`
	ExpectedHeaderPresent *bool    `json:"expected_current_header_present"`
	Expected              []string `json:"expected"`
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
		if packetCount > headerCount && c.HeaderCapable && c.HeaderPresent {
			t.Fatalf("packet callbacks exceed newly decoded headers: %s", c.Name)
		}
		if len(c.BodyLengths) > 0 && len(c.BodyLengths) != 2 {
			t.Fatalf("body frame cardinality mismatch: %s", c.Name)
		}
		bodyLengths := c.BodyLengths
		if len(bodyLengths) == 0 && c.BodyLength > 0 {
			bodyLengths = []int{c.BodyLength}
		}
		if c.Accumulated > 0 && len(bodyLengths) > 0 {
			buffered, needed := c.Accumulated, 0
			header := c.HeaderPresent
			model := make([]string, 0, len(c.Expected))
			for _, body := range bodyLengths {
				if buffered < 22 {
					needed = 22 - buffered
					model = append(model, "buffer_retained_below_minimum")
					break
				}
				if !header {
					model = append(model, "decode_packet_id_u32", "decode_status_u16", "decode_method_utf8_11", "decode_body_type_u8", "decode_body_length_u32")
					if c.HeaderCapable {
						model = append(model, "header_callback_if_supported")
					}
					header = true
					if c.IdentityMatch {
						model = append(model, "conditional_timeout_disarm_after_header")
					}
				}
				needed = 22 + body
				if buffered < needed {
					needed -= buffered
					break
				}
				buffered -= needed
				if c.PacketCapable {
					model = append(model, "complete_packet_callback_after_body")
				}
				needed = 0
				header = false
			}
			if len(bodyLengths) > 1 {
				model = append(model, "producer_loop_next_frame")
			}
			if c.ExpectedBufferedBytes != nil && *c.ExpectedBufferedBytes != buffered {
				t.Fatalf("buffered bytes mismatch %s: got %d", c.Name, buffered)
			}
			if c.ExpectedBytesNeeded != nil && *c.ExpectedBytesNeeded != needed {
				t.Fatalf("bytes needed mismatch %s: got %d", c.Name, needed)
			}
			if c.ExpectedHeaderPresent != nil && *c.ExpectedHeaderPresent != header {
				t.Fatalf("header state mismatch: %s", c.Name)
			}
			// Compare the modeled producer suffix exactly, independent of case names.
			prefix := len(c.Expected) - len(model)
			if prefix < 0 {
				t.Fatalf("model longer than expected: %s", c.Name)
			}
			for i, effect := range model {
				if c.Expected[prefix+i] != effect {
					t.Fatalf("producer event mismatch %s at %d: want %s got %s", c.Name, i, effect, c.Expected[prefix+i])
				}
			}
		}
	}
}
