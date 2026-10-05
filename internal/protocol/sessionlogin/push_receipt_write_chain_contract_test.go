package sessionlogin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type pushReceiptWriteChainFixture struct {
	Status string                      `json:"status"`
	Cases  []pushReceiptWriteChainCase `json:"cases"`
}
type pushReceiptWriteChainCase struct {
	Name             string   `json:"name"`
	OwnerStatus      int      `json:"owner_status"`
	CallerTag        int64    `json:"caller_tag"`
	PacketID         uint32   `json:"packet_id"`
	ExpectedWriteTag int64    `json:"expected_write_tag"`
	ExpectedEffects  []string `json:"expected_effects"`
}

func expectedPushReceiptWriteChain(c pushReceiptWriteChainCase) []string {
	if c.OwnerStatus != 3 {
		return []string{"owner_status_gate_suppresses_packet_access"}
	}
	return []string{"read_packet_data", "encrypt_packet_data", "read_socket", "read_packet_header", "read_packet_id", "derive_write_tag_from_packet_id", "write_data_timeout_minus_one", "toggle_out_segment_timeout_true"}
}
func TestPushReceiptWriteChainFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-push-receipt-write-chain.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f pushReceiptWriteChainFixture
	if err = json.Unmarshal(body, &f); err != nil {
		t.Fatal(err)
	}
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 4 {
		t.Fatalf("fixture header = %#v", f)
	}
	for _, c := range f.Cases {
		if got, want := c.ExpectedEffects, expectedPushReceiptWriteChain(c); !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", c.Name, got, want)
		}
	}
	if !reflect.DeepEqual(f.Cases[0].ExpectedEffects, f.Cases[1].ExpectedEffects) || f.Cases[0].ExpectedWriteTag != f.Cases[1].ExpectedWriteTag {
		t.Fatal("negative and positive caller tags must share packet-ID write path")
	}
	for _, c := range f.Cases {
		if c.OwnerStatus == 3 && c.ExpectedWriteTag != -int64(c.PacketID) {
			t.Errorf("%s write tag = %d, want packet-ID negation %d", c.Name, c.ExpectedWriteTag, -int64(c.PacketID))
		}
	}
}
