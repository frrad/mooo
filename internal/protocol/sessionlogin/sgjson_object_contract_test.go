package sessionlogin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type sgJSONFixture struct {
	Status string       `json:"status"`
	Cases  []sgJSONCase `json:"cases"`
}

type sgJSONCase struct {
	Name                 string                  `json:"name"`
	SubclassProperties   []string                `json:"subclass_properties"`
	SuperclassProperties []string                `json:"superclass_properties"`
	Values               []sgJSONValue           `json:"values"`
	ExpectedProperties   []string                `json:"expected_properties"`
	Expected             map[string]sgJSONResult `json:"expected"`
}

type sgJSONValue struct {
	Name                string `json:"name"`
	Kind                string `json:"kind"`
	ConformsJSON        bool   `json:"conforms_json,omitempty"`
	ConformsNumberArray bool   `json:"conforms_number_array,omitempty"`
	JSONObjectResult    string `json:"json_object_result,omitempty"`
	NumberArrayResult   string `json:"number_array_result,omitempty"`
	ScalarResult        string `json:"scalar_result,omitempty"`
}

type sgJSONResult struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// projectSGJSONObject models the observed SGJsonObject.JSONObject block. The
// descriptors represent protocol objects, rather than treating every map or
// slice as an SGJson implementation.
func projectSGJSONObject(c sgJSONCase) (map[string]sgJSONResult, []string) {
	properties := append(append([]string{}, c.SubclassProperties...), c.SuperclassProperties...)
	out := make(map[string]sgJSONResult, len(c.Values))
	for _, value := range c.Values {
		switch {
		case value.Kind == "nil" || value.Kind == "nsnull":
			out[value.Name] = sgJSONResult{Kind: "nsnull", Value: "NSNull"}
		case value.ConformsJSON:
			out[value.Name] = sgJSONResult{Kind: "json", Value: value.JSONObjectResult}
		case value.ConformsNumberArray:
			out[value.Name] = sgJSONResult{Kind: "number_array", Value: value.NumberArrayResult}
		default:
			out[value.Name] = sgJSONResult{Kind: "scalar", Value: value.ScalarResult}
		}
	}
	return out, properties
}

func TestSGJSONObjectProjectionFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-sgjson-object.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture sgJSONFixture
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Status != "reviewed-static-synthetic" || len(fixture.Cases) != 5 {
		t.Fatalf("header %#v", fixture)
	}
	for _, c := range fixture.Cases {
		got, properties := projectSGJSONObject(c)
		if !reflect.DeepEqual(properties, c.ExpectedProperties) {
			t.Errorf("%s properties=%v want %v", c.Name, properties, c.ExpectedProperties)
		}
		if !reflect.DeepEqual(got, c.Expected) {
			t.Errorf("%s output=%#v want %#v", c.Name, got, c.Expected)
		}
	}
}
