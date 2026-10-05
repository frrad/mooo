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
	Name     string                 `json:"name"`
	Input    map[string]interface{} `json:"input"`
	Expected map[string]interface{} `json:"expected"`
}

// projectSGJSONObject models only the observed SGJsonObject.JSONObject block:
// nil/NSNull become NSNull, nested SGJson values recurse, number arrays use
// their numberArray projection, and ordinary values pass through.
func projectSGJSONObject(input map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(input))
	for key, value := range input {
		switch typed := value.(type) {
		case nil:
			out[key] = "NSNull"
		case map[string]interface{}:
			out[key] = projectSGJSONObject(typed)
		case []interface{}:
			out[key] = typed
		default:
			out[key] = value
		}
	}
	return out
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
	if fixture.Status != "reviewed-static-synthetic" || len(fixture.Cases) != 4 {
		t.Fatalf("header %#v", fixture)
	}
	for _, c := range fixture.Cases {
		if got := projectSGJSONObject(c.Input); !reflect.DeepEqual(got, c.Expected) {
			t.Errorf("%s output=%#v want %#v", c.Name, got, c.Expected)
		}
	}
}
