package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type pushReceiptRequestModelFixture struct {
	Status string                        `json:"status"`
	Cases  []pushReceiptRequestModelCase `json:"cases"`
}
type pushReceiptRequestModelCase struct {
	Name             string   `json:"name"`
	Handler          string   `json:"handler"`
	HeaderMethod     string   `json:"header_method"`
	HeaderPacketID   uint32   `json:"header_packet_id"`
	Revision         *int32   `json:"revision"`
	PlusRevision     *int32   `json:"plus_revision"`
	OwnerStatus      uint8    `json:"owner_status"`
	PacketID         uint32   `json:"packet_id"`
	ExpectedMethod   string   `json:"expected_method"`
	ExpectedPacketID uint32   `json:"expected_packet_id"`
	ExpectedTag      int64    `json:"expected_tag"`
	ExpectedRevision *int32   `json:"expected_revision"`
	ExpectedPlus     *int32   `json:"expected_plus_revision"`
	ConstructorOK    *bool    `json:"constructor_succeeds"`
	ExpectedEffects  []string `json:"expected_effects"`
}

func expectedPushReceiptRequestModel(c pushReceiptRequestModelCase) []string {
	if c.Handler == "send" {
		e := []string{"queue_owner_block", "read_execution_status"}
		if c.OwnerStatus == 3 {
			e = append(e, "read_packet_header", "read_packet_id", "derive_tag_negation", "send_packet_tag")
		}
		return e
	}
	e := []string{"delegate_callback_attempt", "construct_from_header"}
	if c.ConstructorOK != nil && !*c.ConstructorOK {
		return append(e, "constructor_returns_nil", "send_carriage_push_receipt")
	}
	if c.Handler == "hint" {
		e = append(e, "copy_header_method", "copy_header_packet_id")
	} else {
		e = append(e, "set_revision", "set_plus_revision")
	}
	return append(e, "send_carriage_push_receipt")
}
func TestPushReceiptRequestModelFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-push-receipt-request-model.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f pushReceiptRequestModelFixture
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&f); err != nil {
		t.Fatal(err)
	}
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 10 {
		t.Fatalf("fixture header = %#v", f)
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Errorf("duplicate or empty case name %q", c.Name)
		}
		seen[c.Name] = true
		if c.Handler != "hint" && c.Handler != "block_sync" && c.Handler != "send" {
			t.Errorf("unsupported handler %q", c.Handler)
		}
		if (c.Handler == "hint" || c.Handler == "block_sync") && (c.ConstructorOK == nil || *c.ConstructorOK) {
			if c.ExpectedMethod != c.HeaderMethod || c.ExpectedPacketID != c.HeaderPacketID {
				t.Errorf("%s header projection = %q/%d, want %q/%d", c.Name, c.ExpectedMethod, c.ExpectedPacketID, c.HeaderMethod, c.HeaderPacketID)
			}
		}
		if c.Handler == "block_sync" && (c.Revision == nil || c.PlusRevision == nil || c.ExpectedRevision == nil || c.ExpectedPlus == nil || *c.Revision != *c.ExpectedRevision || *c.PlusRevision != *c.ExpectedPlus) {
			if c.ConstructorOK == nil || *c.ConstructorOK {
				t.Errorf("%s revision projection does not match signed-32 inputs", c.Name)
			}
		}
		if c.ConstructorOK != nil && !*c.ConstructorOK && (c.ExpectedMethod != "" || c.ExpectedPacketID != 0 || c.ExpectedRevision != nil || c.ExpectedPlus != nil) {
			t.Errorf("%s failed constructor must have absent output fields", c.Name)
		}
		if c.Handler == "send" && c.OwnerStatus == 3 && c.ExpectedTag != -int64(c.PacketID) {
			t.Errorf("%s tag = %d, want %d", c.Name, c.ExpectedTag, -int64(c.PacketID))
		}
		if got, want := c.ExpectedEffects, expectedPushReceiptRequestModel(c); !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", c.Name, got, want)
		}
	}
}
