package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type pushReceiptBodyFixture struct {
	Status string                `json:"status"`
	Cases  []pushReceiptBodyCase `json:"cases"`
}
type pushReceiptBodyCase struct {
	Name           string   `json:"name"`
	Model          string   `json:"model"`
	Method         string   `json:"method"`
	PacketID       uint32   `json:"packet_id"`
	Revision       int32    `json:"revision"`
	PlusRevision   int32    `json:"plus_revision"`
	ExpectedFields []string `json:"expected_object_fields"`
}

func expectedPushReceiptBodyFields(c pushReceiptBodyCase) []string {
	switch c.Model {
	case "hint_push_receipt":
		return []string{"method", "packetId"}
	case "block_sync_push_receipt":
		return []string{"method", "packetId", "revision", "plusRevision"}
	default:
		return nil
	}
}

func TestPushReceiptBodySerializationFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-push-receipt-body-serialization.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f pushReceiptBodyFixture
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err = d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	if f.Status != "reviewed-static-object-field-inventory-unexecuted-runtime" || len(f.Cases) != 4 {
		t.Fatalf("fixture header = %#v", f)
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty case %q", c.Name)
		}
		seen[c.Name] = true
		if c.Model != "hint_push_receipt" && c.Model != "block_sync_push_receipt" {
			t.Fatalf("invalid model %q", c.Model)
		}
		if got, want := c.ExpectedFields, expectedPushReceiptBodyFields(c); !reflect.DeepEqual(got, want) {
			t.Errorf("%s fields = %v, want %v", c.Name, got, want)
		}
	}
}
