package sessionlogin

import (
	"errors"
	"reflect"
	"testing"
)

type syntheticSGJSON struct{ value any }

func (v syntheticSGJSON) JSONObject() any { return v.value }

type syntheticSGNumberArray struct{ value []any }

func (v syntheticSGNumberArray) NumberArray() any { return v.value }

type syntheticBothSGProtocols struct{}

func (syntheticBothSGProtocols) JSONObject() any  { return map[string]any{"winner": "json"} }
func (syntheticBothSGProtocols) NumberArray() any { return []any{"number-array"} }

type syntheticNilSGJSON struct{}

func (syntheticNilSGJSON) JSONObject() any { return nil }

type syntheticProperties struct{ properties []SGJSONProperty }

func (v syntheticProperties) JSONProperties() []SGJSONProperty { return v.properties }

func TestProjectSGJSONObjectSourceFixtureCases(t *testing.T) {
	cases := []struct {
		name  string
		input any
		want  any
	}{
		{name: "nil becomes explicit null", input: nil, want: SGJSONNull{}},
		{name: "explicit null remains explicit null", input: SGJSONNull{}, want: SGJSONNull{}},
		{name: "scalar zero is retained", input: map[string]any{"zero": 0}, want: map[string]any{"zero": 0}},
		{name: "nested SGJSON is assigned directly", input: syntheticSGJSON{value: map[string]any{"revision": 0}}, want: map[string]any{"revision": 0}},
		{name: "number array keeps order", input: syntheticSGNumberArray{value: []any{0, -1, uint64(4294967295)}}, want: []any{0, -1, uint64(4294967295)}},
		{name: "SGJSON wins protocol precedence", input: syntheticBothSGProtocols{}, want: map[string]any{"winner": "json"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ProjectSGJSONObject(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("projection=%#v want %#v", got, tc.want)
			}
		})
	}
}

func TestProjectSGJSONObjectRejectsNilProtocolResults(t *testing.T) {
	if _, err := ProjectSGJSONObject(syntheticNilSGJSON{}); !errors.Is(err, ErrSGJSONNilProtocolResult) {
		t.Fatalf("nil protocol result error=%v, want %v", err, ErrSGJSONNilProtocolResult)
	}
}

func TestProjectSGJSONPropertiesPreservesDynamicAndInheritedCollisionRules(t *testing.T) {
	dynamic := syntheticProperties{properties: []SGJSONProperty{
		{Name: "method", Value: "HINT"},
		{Name: "revision", Value: int32(0)},
		{Name: "missingValue", Value: nil},
	}}
	inherited := []SGJSONProperty{{Name: "method", Value: "BASE"}, {Name: "packetId", Value: uint32(0)}}
	got, err := ProjectSGJSONProperties(dynamic.JSONProperties(), inherited)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"method":       "BASE",
		"packetId":     uint32(0),
		"revision":     int32(0),
		"missingValue": SGJSONNull{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("properties=%#v want %#v", got, want)
	}
}

func TestProjectSGJSONObjectPropertySource(t *testing.T) {
	input := syntheticProperties{properties: []SGJSONProperty{{Name: "zero", Value: 0}, {Name: "null", Value: nil}}}
	got, err := ProjectSGJSONProperties(input.JSONProperties())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"zero": 0, "null": SGJSONNull{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("projection=%#v want %#v", got, want)
	}
}

func TestProjectSGJSONNamedPropertiesFiltersUndeclaredValues(t *testing.T) {
	names := []string{"child", "base", "missing"}
	values := map[string]any{
		"child":      "child-value",
		"base":       int32(0),
		"undeclared": "ignored",
	}
	got, err := ProjectSGJSONNamedProperties(names, values)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"child":   "child-value",
		"base":    int32(0),
		"missing": SGJSONNull{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("named projection=%#v want %#v", got, want)
	}
	if _, ok := got["undeclared"]; ok {
		t.Fatal("undeclared property was projected")
	}
}
