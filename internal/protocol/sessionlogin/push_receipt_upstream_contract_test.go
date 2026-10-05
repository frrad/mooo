package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type pushReceiptUpstreamFixture struct {
	Status string                    `json:"status"`
	Cases  []pushReceiptUpstreamCase `json:"cases"`
}

type pushReceiptUpstreamCase struct {
	Name                string   `json:"name"`
	Handler             string   `json:"handler"`
	DelegateResponds    bool     `json:"delegate_responds"`
	PacketHeaderPresent bool     `json:"packet_header_present"`
	ExpectedEffects     []string `json:"expected_effects"`
}

func expectedPushReceiptUpstream(c pushReceiptUpstreamCase) []string {
	effects := []string{"retain_notice", "retain_packet_header"}
	if c.DelegateResponds {
		if c.Handler == "hint" {
			effects = append(effects, "delegate_hint_notice")
		} else {
			effects = append(effects, "delegate_block_sync_notice")
		}
	}
	if c.Handler == "hint" {
		effects = append(effects, "construct_hint_packet")
	} else {
		effects = append(effects, "construct_block_sync_packet")
	}
	return append(effects, "send_carriage_push_receipt")
}

func TestPushReceiptUpstreamFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-push-receipt-upstream.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture pushReceiptUpstreamFixture
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Status != "reviewed-static-unexecuted-runtime" || len(fixture.Cases) != 5 {
		t.Fatalf("fixture header = %#v", fixture)
	}
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" || seen[c.Name] || (c.Handler != "hint" && c.Handler != "block_sync") {
			t.Fatalf("invalid case %#v", c)
		}
		seen[c.Name] = true
		if got, want := c.ExpectedEffects, expectedPushReceiptUpstream(c); !reflect.DeepEqual(got, want) {
			t.Errorf("%s effects = %v, want %v", c.Name, got, want)
		}
	}
}
