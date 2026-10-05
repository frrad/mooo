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
	Name           string             `json:"name"`
	InputKind      string             `json:"input_kind"`
	Input          map[string]*string `json:"input,omitempty"`
	OpaqueInput    *string            `json:"opaque_input,omitempty"`
	ExpectedOpaque *string            `json:"expected_opaque,omitempty"`
	BaseRemove     []string           `json:"base_remove,omitempty"`
	Mappings       [][2]string        `json:"mappings,omitempty"`
	Expected       map[string]*string `json:"expected,omitempty"`
	Effects        []string           `json:"effects"`
}

type receiptJSONMappingResult struct {
	Dictionary map[string]*string
	Opaque     *string
	IsOpaque   bool
}

func projectReceiptJSONMapping(c receiptJSONMappingCase) receiptJSONMappingResult {
	if c.InputKind != "dictionary" {
		return receiptJSONMappingResult{Opaque: c.OpaqueInput, IsOpaque: true}
	}
	out := make(map[string]*string, len(c.Input))
	for k, v := range c.Input {
		out[k] = v
	}
	for _, key := range c.BaseRemove {
		delete(out, key)
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
	return receiptJSONMappingResult{Dictionary: out}
}

func expectedReceiptJSONMappingEffects(c receiptJSONMappingCase) []string {
	if c.InputKind != "dictionary" {
		return []string{"return_super_result_unchanged"}
	}
	effects := []string{"mutable_dictionary"}
	working := make(map[string]*string, len(c.Input))
	for k, v := range c.Input {
		working[k] = v
	}
	for range c.BaseRemove {
		effects = append(effects, "remove_static_property")
	}
	for _, key := range c.BaseRemove {
		delete(working, key)
	}
	for _, mapping := range c.Mappings {
		_, source := mapping[0], mapping[1]
		value, present := working[source]
		if !present {
			effects = append(effects, "source_lookup_absent", "skip_assignment", "no_source_removal")
			continue
		}
		effects = append(effects, "source_lookup_present")
		if value == nil {
			effects = append(effects, "nsnull_guard")
		} else {
			effects = append(effects, "assign_destination")
		}
		effects = append(effects, "remove_source")
		if value != nil {
			working[mapping[0]] = value
		}
		delete(working, source)
	}
	return effects
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
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 8 {
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
		if c.InputKind == "non_dictionary" && c.OpaqueInput == nil {
			t.Errorf("%s missing opaque superclass result", c.Name)
		}
		got := projectReceiptJSONMapping(c)
		if c.InputKind == "non_dictionary" {
			if !got.IsOpaque || !reflect.DeepEqual(got.Opaque, c.ExpectedOpaque) {
				t.Errorf("%s opaque result=%v want %v", c.Name, got, c.ExpectedOpaque)
			}
		} else if !reflect.DeepEqual(got.Dictionary, c.Expected) {
			t.Errorf("%s output=%v want %v", c.Name, got, c.Expected)
		}
		if got := c.Effects; !reflect.DeepEqual(got, expectedReceiptJSONMappingEffects(c)) {
			t.Errorf("%s effects=%v want %v", c.Name, got, expectedReceiptJSONMappingEffects(c))
		}
	}
}

func TestPushReceiptJSONMappingOpaquePassthroughPreservesIdentity(t *testing.T) {
	sentinel := "opaque-superclass-value"
	c := receiptJSONMappingCase{InputKind: "non_dictionary", OpaqueInput: &sentinel}
	got := projectReceiptJSONMapping(c)
	if !got.IsOpaque {
		t.Fatal("non-dictionary projection lost opaque result")
	}
	if got.Opaque != c.OpaqueInput {
		t.Fatalf("opaque identity changed: got %p want %p", got.Opaque, c.OpaqueInput)
	}
}
