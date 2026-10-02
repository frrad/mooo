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
	"forward_nil_error":              true,
	"no_receive_timeout_arm":         true,
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
	if len(v.Cases) != 3 {
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
	}
	return v, nil
}

func TestSendOrderContractSchema(t *testing.T) {
	v, err := loadSendOrderContract(filepath.Join("testdata", "reconnect", "rc-q5-send-order.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Cases[0].ExpectedEffects; len(got) != 9 || got[4] != "send_packet" || got[5] != "packet_data" || got[6] != "encrypt_packet_data" || got[7] != "socket_write_timeout_minus_one" || got[8] != "enable_out_segment_timeout" {
		t.Fatalf("completion send order=%v", got)
	}
	if got := v.Cases[1].ExpectedEffects; len(got) != 7 || got[2] != "send_packet" || got[6] != "enable_out_segment_timeout" {
		t.Fatalf("fire-and-forget send order=%v", got)
	}
	if got := v.Cases[2].ExpectedEffects; len(got) != 4 || got[3] != "no_receive_timeout_arm" {
		t.Fatalf("non-status-3 effects=%v", got)
	}
}
