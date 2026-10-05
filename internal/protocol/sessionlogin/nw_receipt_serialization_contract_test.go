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
	Name                string   `json:"name"`
	Operation           string   `json:"operation"`
	Model               string   `json:"model"`
	CompletionError     string   `json:"completion_error"`
	ExpectedEffects     []string `json:"expected_effects"`
	ExpectedWireBody    string   `json:"expected_wire_body"`
	ExpectedCorrelation string   `json:"expected_correlation"`
}

func expectedNWReceiptEffects(c nwReceiptCase) []string {
	switch c.Operation {
	case "nw_send":
		return []string{"packet_data", "bridge_data", "encrypt_packet_data", "bridge_encrypted_data", "nw_send_default_message_complete", "toggle_out_timeout_false"}
	case "completion":
		return []string{"send_completion_receives_nw_error_or_nil", "pending_correlation_unproven"}
	case "body_inventory":
		return []string{"object_fields_available_to_packet_data", "wire_serialization_unproven"}
	default:
		return nil
	}
}

func TestNWReceiptSerializationFixture(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-push-receipt-nw-serialization.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f nwReceiptFixture
	d := json.NewDecoder(bytes.NewReader(b))
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
		if got, want := c.ExpectedEffects, expectedNWReceiptEffects(c); !reflect.DeepEqual(got, want) {
			t.Errorf("%s=%v want %v", c.Name, got, want)
		}
		if c.Operation == "completion" && c.CompletionError != "nil" && c.CompletionError != "nw_error" {
			t.Errorf("%s completion input %q", c.Name, c.CompletionError)
		}
		if c.Operation == "nw_send" && c.ExpectedWireBody != "unresolved" {
			t.Errorf("%s wire body=%q", c.Name, c.ExpectedWireBody)
		}
		if c.Operation == "completion" && c.ExpectedCorrelation != "unresolved" {
			t.Errorf("%s correlation=%q", c.Name, c.ExpectedCorrelation)
		}
	}
}
