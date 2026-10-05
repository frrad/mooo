package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type pushReceiptObjectFieldsFixture struct {
	Status string                        `json:"status"`
	Cases  []pushReceiptObjectFieldsCase `json:"cases"`
}
type pushReceiptObjectFieldsCase struct {
	Name           string   `json:"name"`
	Model          string   `json:"model"`
	ExpectedFields []string `json:"expected_object_fields"`
}

func expectedPushReceiptObjectFields(c pushReceiptObjectFieldsCase) []string {
	switch c.Model {
	case "hint_push_receipt":
		return []string{"method", "packetId"}
	case "block_sync_push_receipt":
		return []string{"method", "packetId", "revision", "plusRevision"}
	default:
		return nil
	}
}

func TestPushReceiptObjectFieldInventoryFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-push-receipt-object-fields.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f pushReceiptObjectFieldsFixture
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err = d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	if f.Status != "reviewed-static-object-field-inventory-unexecuted-runtime" || len(f.Cases) != 2 {
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
		if got, want := c.ExpectedFields, expectedPushReceiptObjectFields(c); !reflect.DeepEqual(got, want) {
			t.Errorf("%s fields = %v, want %v", c.Name, got, want)
		}
	}
}
