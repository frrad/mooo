package sessionlogin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type sendOrderContract struct {
	Status string          `json:"status"`
	Cases  []sendOrderCase `json:"cases"`
}

type sendOrderCase struct {
	Name            string   `json:"name"`
	ProducerStatus  uint8    `json:"producer_status"`
	Completion      bool     `json:"completion_present"`
	CryptoPresent   bool     `json:"crypto_present"`
	ExpectedEffects []string `json:"expected_effects"`
	RemainingGaps   []string `json:"remaining_gaps"`
}

var knownSendOrderEffect = map[string]bool{
	"allocate_packet":                true,
	"derive_packet_tag":              true,
	"register_completion_by_uid":     true,
	"register_packet_uid":            true,
	"send_packet":                    true,
	"packet_data":                    true,
	"encrypt_packet_data":            true,
	"socket_write_timeout_minus_one": true,
	"enable_out_segment_timeout":     true,
	"forward_nil_packet":             true,
	"forward_producer_error":         true,
	"no_completion_callback":         true,
	"no_send":                        true,
	"no_receive_timeout_arm":         true,
	"arm_receive_header_timeout":     true,
}

func loadSendOrderContract(path string) (sendOrderContract, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return sendOrderContract{}, err
	}
	var v sendOrderContract
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, err
	}
	if v.Status != "reviewed-static-unexecuted-runtime" {
		return v, fmt.Errorf("status=%q", v.Status)
	}
	if len(v.Cases) != 4 {
		return v, fmt.Errorf("cases=%d", len(v.Cases))
	}
	seen := map[string]bool{}
	for _, c := range v.Cases {
		if c.Name == "" || seen[c.Name] || len(c.ExpectedEffects) == 0 || len(c.RemainingGaps) == 0 {
			return v, fmt.Errorf("invalid case %q", c.Name)
		}
		seen[c.Name] = true
		for _, effect := range c.ExpectedEffects {
			if !knownSendOrderEffect[effect] {
				return v, fmt.Errorf("unknown effect %q", effect)
			}
		}
		if !c.CryptoPresent {
			return v, fmt.Errorf("crypto scope must be explicit supported path: %q", c.Name)
		}
		want := []string{"allocate_packet", "derive_packet_tag"}
		if c.ProducerStatus == 3 {
			if c.Completion {
				want = append(want, "register_completion_by_uid", "register_packet_uid")
			}
			want = append(want, "send_packet", "packet_data", "encrypt_packet_data", "socket_write_timeout_minus_one", "enable_out_segment_timeout", "arm_receive_header_timeout")
		} else if c.Completion {
			want = append(want, "forward_nil_packet", "forward_producer_error", "no_receive_timeout_arm")
		} else {
			want = append(want, "no_completion_callback", "no_send", "no_receive_timeout_arm")
		}
		if len(want) != len(c.ExpectedEffects) {
			return v, fmt.Errorf("effect count mismatch %q: got=%v want=%v", c.Name, c.ExpectedEffects, want)
		}
		for i := range want {
			if c.ExpectedEffects[i] != want[i] {
				return v, fmt.Errorf("effect order mismatch %q: got=%v want=%v", c.Name, c.ExpectedEffects, want)
			}
		}
	}
	return v, nil
}

func TestSendOrderContractSchema(t *testing.T) {
	v, err := loadSendOrderContract(filepath.Join("testdata", "reconnect", "rc-q5-send-order.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Cases) != 4 {
		t.Fatalf("cases=%d", len(v.Cases))
	}
}
