package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type receiptJSONMappingFixture struct {
	Status string                   `json:"status"`
	Cases  []receiptJSONMappingCase `json:"cases"`
}

type receiptJSONMappingCase struct {
	Name      string             `json:"name"`
	InputKind string             `json:"input_kind"`
	Input     map[string]*string `json:"input,omitempty"`
	Mappings  [][2]string        `json:"mappings,omitempty"`
	Expected  map[string]*string `json:"expected,omitempty"`
	Effects   []string           `json:"effects"`
}

func projectReceiptJSONMapping(c receiptJSONMappingCase) map[string]*string {
	if c.InputKind != "dictionary" {
		return nil
	}
	out := make(map[string]*string, len(c.Input))
	for k, v := range c.Input {
		out[k] = v
	}
	for _, mapping := range c.Mappings {
		destination, source := mapping[0], mapping[1]
		value, present := out[source]
		if !present {
			continue
		}
		if value != nil {
			out[destination] = value
		}
		delete(out, source)
	}
	return out
}

func TestPushReceiptJSONMappingFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-push-receipt-json-mapping.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f receiptJSONMappingFixture
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 5 {
		t.Fatalf("header %#v", f)
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty %q", c.Name)
		}
		seen[c.Name] = true
		if c.InputKind != "dictionary" && c.InputKind != "non_dictionary" {
			t.Fatalf("%s input_kind=%q", c.Name, c.InputKind)
		}
		if got := projectReceiptJSONMapping(c); !reflect.DeepEqual(got, c.Expected) {
			t.Errorf("%s output=%v want %v", c.Name, got, c.Expected)
		}
		if len(c.Effects) == 0 {
			t.Errorf("%s has no guarded effects", c.Name)
		}
	}
}
