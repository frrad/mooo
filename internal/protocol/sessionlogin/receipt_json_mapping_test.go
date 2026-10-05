package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type receiptJSONMappingProjectionFixture struct {
	Status string                             `json:"status"`
	Cases  []receiptJSONMappingProjectionCase `json:"cases"`
}

type receiptJSONMappingProjectionCase struct {
	Name           string             `json:"name"`
	InputKind      string             `json:"input_kind"`
	Input          map[string]*string `json:"input,omitempty"`
	BaseRemove     []string           `json:"base_remove,omitempty"`
	Mappings       [][2]string        `json:"mappings,omitempty"`
	Expected       map[string]*string `json:"expected,omitempty"`
	OpaqueInput    string             `json:"opaque_input,omitempty"`
	ExpectedOpaque string             `json:"expected_opaque,omitempty"`
	Effects        []string           `json:"effects"`
}

func TestReceiptJSONMappingFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-push-receipt-json-mapping.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture receiptJSONMappingProjectionFixture
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Status != "reviewed-static-unexecuted-runtime" || len(fixture.Cases) != 8 {
		t.Fatalf("header %#v", fixture)
	}
	for _, testCase := range fixture.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			if testCase.InputKind != "dictionary" && testCase.InputKind != "non_dictionary" {
				t.Fatalf("input kind %q", testCase.InputKind)
			}
			if len(testCase.Effects) == 0 {
				t.Fatal("fixture case has no guarded effects")
			}
			var input any
			if testCase.InputKind == "dictionary" {
				input = make(map[string]any, len(testCase.Input))
				for key, value := range testCase.Input {
					if value != nil {
						input.(map[string]any)[key] = *value
					} else {
						input.(map[string]any)[key] = SGJSONNull{}
					}
				}
			} else {
				input = &struct{ value string }{value: "opaque-superclass-value"}
			}
			mappings := make([]ReceiptJSONMapping, 0, len(testCase.Mappings))
			for _, pair := range testCase.Mappings {
				mappings = append(mappings, ReceiptJSONMapping{Destination: pair[0], Source: pair[1]})
			}
			got := MapReceiptJSONObject(input, testCase.BaseRemove, mappings)
			if testCase.InputKind == "non_dictionary" {
				if got != input {
					t.Fatalf("opaque result identity changed: %p != %p", got, input)
				}
				return
			}
			want := make(map[string]any, len(testCase.Expected))
			for key, value := range testCase.Expected {
				if value != nil {
					want[key] = *value
				} else {
					want[key] = SGJSONNull{}
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("mapping=%#v want %#v", got, want)
			}
		})
	}
}

func TestMapReceiptJSONObjectPreservesExistingDestinationOnNullSource(t *testing.T) {
	input := map[string]any{"dst": "keep", "src": nil, "zero": int32(0)}
	got := MapReceiptJSONObject(input, nil, []ReceiptJSONMapping{
		{Destination: "dst", Source: "src"},
		{Destination: "copied", Source: "zero"},
		{Destination: "missing-destination", Source: "missing-source"},
	})
	want := map[string]any{"dst": "keep", "copied": int32(0)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mapping=%#v want %#v", got, want)
	}
}

func TestMapReceiptJSONObjectPreservesTypedNilSourceAsOpaque(t *testing.T) {
	var source *struct{ marker int }
	input := map[string]any{"dst": "keep", "src": source}
	got, ok := MapReceiptJSONObject(input, nil, []ReceiptJSONMapping{{Destination: "dst", Source: "src"}}).(map[string]any)
	if !ok || got["dst"] != source || got["src"] != nil {
		t.Fatalf("typed nil mapping=%#v want destination identity and removed source", got)
	}
}

func TestMapReceiptJSONObjectPreservesOrdinaryNilContainers(t *testing.T) {
	var nilMap map[string]any
	var nilSlice []any
	input := map[string]any{"map": nilMap, "slice": nilSlice}
	got, ok := MapReceiptJSONObject(input, nil, []ReceiptJSONMapping{
		{Destination: "mapCopy", Source: "map"},
		{Destination: "sliceCopy", Source: "slice"},
	}).(map[string]any)
	if !ok || !reflect.DeepEqual(got["mapCopy"], nilMap) || !reflect.DeepEqual(got["sliceCopy"], nilSlice) {
		t.Fatalf("nil container mapping=%#v want opaque identities", got)
	}
}

func TestMapReceiptJSONObjectComposesWithOrdinaryTypedNilProjection(t *testing.T) {
	var ordinary *struct{ marker int }
	projected, err := ProjectSGJSONObject(ordinary)
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"src": projected}
	got, ok := MapReceiptJSONObject(input, nil, []ReceiptJSONMapping{{Destination: "dst", Source: "src"}}).(map[string]any)
	if !ok || got["dst"] != ordinary || got["src"] != nil {
		t.Fatalf("ordinary typed nil composition=%#v want destination identity and removed source", got)
	}
}

func TestMapReceiptJSONObjectPreservesOpaqueValueIdentity(t *testing.T) {
	value := &struct{ marker int }{marker: 7}
	input := map[string]any{"src": value}
	got, ok := MapReceiptJSONObject(input, nil, []ReceiptJSONMapping{{Destination: "dst", Source: "src"}}).(map[string]any)
	if !ok || got["dst"] != value || got["src"] != nil {
		t.Fatalf("opaque mapping=%#v, want destination identity and removed source", got)
	}
	if input["src"] != value {
		t.Fatal("input dictionary was mutated")
	}
}

func TestMapReceiptJSONObjectComposesWithSGJSONNilProjection(t *testing.T) {
	projected, err := ProjectSGJSONObject(nil)
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"src": projected, "dst": "retained"}
	got := MapReceiptJSONObject(input, nil, []ReceiptJSONMapping{{Destination: "dst", Source: "src"}})
	want := map[string]any{"dst": "retained"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("projected null mapping=%#v want %#v", got, want)
	}
}
