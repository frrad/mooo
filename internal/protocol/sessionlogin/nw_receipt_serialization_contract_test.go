package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type nwReceiptFixture struct {
	Status string          `json:"status"`
	Cases  []nwReceiptCase `json:"cases"`
}
type nwReceiptCase struct {
	Name                 string   `json:"name"`
	Operation            string   `json:"operation"`
	PacketDataPresent    bool     `json:"packet_data_present"`
	EncryptedDataPresent bool     `json:"encrypted_data_present"`
	ConnectionPresent    bool     `json:"connection_present"`
	ExpectedEffects      []string `json:"expected_effects"`
}

func expectedNWSendEffects(c nwReceiptCase) []string {
	if !c.PacketDataPresent {
		return []string{"packet_data_nil_return"}
	}
	effects := []string{"read_body", "bson_data", "set_header_body_length", "read_header_data", "append_header_data", "append_bson_body_data", "bridge_packet_data_to_foundation_data", "encrypt_packet_data"}
	if c.EncryptedDataPresent {
		effects = append(effects, "bridge_encrypted_data")
	} else {
		effects = append(effects, "encrypted_data_nil_path")
	}
	if c.ConnectionPresent {
		effects = append(effects, "nw_send_default_message_complete")
	}
	return append(effects, "toggle_out_timeout_true")
}

func TestNWReceiptSerializationFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-push-receipt-nw-serialization.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f nwReceiptFixture
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err = d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 4 {
		t.Fatalf("header %#v", f)
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty %q", c.Name)
		}
		seen[c.Name] = true
		if c.Operation != "nw_send" {
			t.Fatalf("%s operation=%q", c.Name, c.Operation)
		}
		if got, want := c.ExpectedEffects, expectedNWSendEffects(c); !reflect.DeepEqual(got, want) {
			t.Errorf("%s effects=%v want %v", c.Name, got, want)
		}
	}
}
