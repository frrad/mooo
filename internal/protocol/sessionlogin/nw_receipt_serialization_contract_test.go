package sessionlogin

import (
	"bytes"
	"encoding/hex"
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
	Name                     string   `json:"name"`
	Operation                string   `json:"operation"`
	BodyLength               int      `json:"body_length"`
	PacketDataPresent        bool     `json:"packet_data_present"`
	EncryptedDataPresent     bool     `json:"encrypted_data_present"`
	ConnectionPresent        bool     `json:"connection_present"`
	ExpectedEffects          []string `json:"expected_effects"`
	FirstBSON                string   `json:"first_bson,omitempty"`
	SecondBSON               string   `json:"second_bson,omitempty"`
	HeaderData               string   `json:"header_data,omitempty"`
	ExpectedPacketData       string   `json:"expected_packet_data,omitempty"`
	ExpectedHeaderBodyLength int      `json:"expected_header_body_length,omitempty"`
	ExpectedCapacity         int      `json:"expected_capacity,omitempty"`
}

func packetDataBytes(t *testing.T, c nwReceiptCase) []byte {
	t.Helper()
	header, err := hex.DecodeString(c.HeaderData)
	if err != nil {
		t.Fatalf("%s header_data: %v", c.Name, err)
	}
	if c.BodyLength == 0 {
		return header
	}
	body, err := hex.DecodeString(c.SecondBSON)
	if err != nil {
		t.Fatalf("%s second_bson: %v", c.Name, err)
	}
	return append(header, body...)
}

func expectedPacketDataEffects(c nwReceiptCase) []string {
	effects := []string{"read_body", "first_bson_data", "set_header_body_length", "read_header_data", "read_header_body_length_for_capacity", "allocate_mutable_data", "append_header_data", "check_bson_length"}
	if c.BodyLength == 0 {
		return append(effects, "skip_body_append_zero_length")
	}
	return append(effects, "reread_body", "second_bson_data", "append_bson_body_data")
}

func expectedNWSendEffects(c nwReceiptCase) []string {
	if !c.PacketDataPresent {
		return []string{"packet_data_nil_return"}
	}
	effects := []string{"packet_data", "bridge_packet_data_to_foundation_data", "encrypt_packet_data"}
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
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 8 {
		t.Fatalf("header %#v", f)
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty %q", c.Name)
		}
		seen[c.Name] = true
		if c.Operation != "nw_send" && c.Operation != "packet_data" {
			t.Fatalf("%s operation=%q", c.Name, c.Operation)
		}
		if c.BodyLength < 0 {
			t.Fatalf("%s body length=%d", c.Name, c.BodyLength)
		}
		if c.Operation == "packet_data" {
			first, err := hex.DecodeString(c.FirstBSON)
			if err != nil {
				t.Fatalf("%s first_bson: %v", c.Name, err)
			}
			if len(first) != c.BodyLength {
				t.Errorf("%s first BSON length=%d want %d", c.Name, len(first), c.BodyLength)
			}
			if c.ExpectedHeaderBodyLength != c.BodyLength {
				t.Errorf("%s header body length=%d want %d", c.Name, c.ExpectedHeaderBodyLength, c.BodyLength)
			}
			if c.ExpectedCapacity != c.BodyLength+22 {
				t.Errorf("%s capacity=%d want %d", c.Name, c.ExpectedCapacity, c.BodyLength+22)
			}
			got := hex.EncodeToString(packetDataBytes(t, c))
			if got != c.ExpectedPacketData {
				t.Errorf("%s packet data=%q want %q", c.Name, got, c.ExpectedPacketData)
			}
		}
		want := expectedNWSendEffects(c)
		if c.Operation == "packet_data" {
			want = expectedPacketDataEffects(c)
		}
		if got := c.ExpectedEffects; !reflect.DeepEqual(got, want) {
			t.Errorf("%s effects=%v want %v", c.Name, got, want)
		}
	}
}
