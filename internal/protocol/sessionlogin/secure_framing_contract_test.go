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
	Name                        string   `json:"name"`
	CryptoPresent               bool     `json:"crypto_present"`
	ReadTag                     int      `json:"read_tag"`
	PrefixValue                 *uint32  `json:"prefix_value"`
	Accumulated                 int      `json:"accumulated_bytes"`
	BodyLength                  int      `json:"body_length"`
	BodyLengths                 []int    `json:"body_lengths"`
	HeaderPresent               bool     `json:"current_header_present"`
	HeaderCapable               bool     `json:"delegate_supports_header"`
	PacketCapable               bool     `json:"delegate_supports_packet"`
	IdentityMatch               bool     `json:"timeout_identity_matches"`
	DelegateIsAgent             bool     `json:"delegate_is_agent"`
	ExpectedBufferedBytes       *int     `json:"expected_buffered_bytes"`
	ExpectedConsumerBytesNeeded *int     `json:"expected_consumer_bytes_needed"`
	ExpectedHeaderPresent       *bool    `json:"expected_current_header_present"`
	Expected                    []string `json:"expected"`
}

var secureEffects = map[string]bool{"header_read_length_4": true, "header_read_length_22": true, "header_timeout_minus_one": true, "header_tag_zero": true, "header_prefix_zero": true, "header_prefix_nonzero": true, "read_header_again": true, "read_body_length": true, "supply_raw_data": true, "route_body": true, "decrypt_accumulated": true, "header_callback_if_supported": true, "complete_packet_callback_after_body": true, "conditional_timeout_disarm_after_header": true, "decode_packet_id_u32": true, "decode_status_u16": true, "decode_method_utf8_11": true, "decode_body_type_u8": true, "decode_body_length_u32": true, "buffer_retained_below_minimum": true, "producer_return_zero_below_minimum": true, "producer_return_remaining_bytes": true}

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
	if v.Status != "reviewed-static-unexecuted-runtime" || v.Question != "RC-Q5" || len(v.Cases) != 16 {
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
		bodyLengths := c.BodyLengths
		if len(bodyLengths) == 0 && c.BodyLength > 0 {
			bodyLengths = []int{c.BodyLength}
		}
		model := make([]string, 0, len(c.Expected))
		buffered, needed := c.Accumulated, 0
		header := c.HeaderPresent
		for _, body := range bodyLengths {
			if body < 0 {
				t.Fatalf("negative body length: %s", c.Name)
			}
			if buffered < 22 {
				needed = 22 - buffered
				model = append(model, "buffer_retained_below_minimum", "producer_return_zero_below_minimum")
				break
			}
			if !header {
				model = append(model, "decode_packet_id_u32", "decode_status_u16", "decode_method_utf8_11", "decode_body_type_u8", "decode_body_length_u32")
				if c.HeaderCapable {
					model = append(model, "header_callback_if_supported")
				}
				header = true
				if c.IdentityMatch && c.HeaderCapable && c.DelegateIsAgent {
					model = append(model, "conditional_timeout_disarm_after_header")
				}
			}
			needed = 22 + body
			if buffered < needed {
				needed -= buffered
				model = append(model, "producer_return_remaining_bytes")
				break
			}
			buffered -= needed
			if c.PacketCapable {
				model = append(model, "complete_packet_callback_after_body")
			}
			needed = 0
			header = false
		}
		transport := []string{}
		if c.ReadTag == 0 {
			transport = append(transport, "header_read_length_22", "header_timeout_minus_one", "header_tag_zero")
			if c.CryptoPresent {
				transport[0] = "header_read_length_4"
				if c.PrefixValue == nil {
					t.Fatalf("crypto header prefix missing: %s", c.Name)
				}
				if *c.PrefixValue == 0 {
					transport = append(transport, "header_prefix_zero", "read_header_again")
				} else {
					transport = append(transport, "header_prefix_nonzero", "read_body_length")
				}
			} else {
				transport = append(transport, "supply_raw_data")
			}
		} else {
			transport = append(transport, "route_body")
			if c.CryptoPresent {
				transport = append(transport, "decrypt_accumulated")
			}
			transport = append(transport, "supply_raw_data")
		}
		want := append(transport, model...)
		if len(c.Expected) != len(want) {
			t.Fatalf("event count mismatch %s: want %d got %d", c.Name, len(want), len(c.Expected))
		}
		for i, effect := range want {
			if c.Expected[i] != effect {
				t.Fatalf("event mismatch %s at %d: want %s got %s", c.Name, i, effect, c.Expected[i])
			}
		}
		if c.ExpectedBufferedBytes != nil && *c.ExpectedBufferedBytes != buffered {
			t.Fatalf("buffered bytes mismatch %s: got %d", c.Name, buffered)
		}
		if c.ExpectedConsumerBytesNeeded != nil && *c.ExpectedConsumerBytesNeeded != needed {
			t.Fatalf("consumer bytes needed mismatch %s: got %d", c.Name, needed)
		}
		if c.ExpectedHeaderPresent != nil && *c.ExpectedHeaderPresent != header {
			t.Fatalf("header state mismatch: %s", c.Name)
		}

	}
}
