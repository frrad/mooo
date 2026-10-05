package sessionlogin

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

type foundationInt32KVCFixture struct {
	Status   string                     `json:"status"`
	Platform string                     `json:"platform"`
	Property foundationInt32KVCProperty `json:"property"`
	Cases    []foundationInt32KVCCase   `json:"cases"`
}

type foundationInt32KVCProperty struct {
	Name     string `json:"name"`
	Encoding string `json:"encoding"`
	Width    int    `json:"width"`
}

type foundationInt32KVCCase struct {
	Name          string `json:"name"`
	InputKind     string `json:"input_kind"`
	Input         string `json:"input"`
	InputClass    string `json:"input_class"`
	InputObjCType string `json:"input_objc_type"`
	InitialValue  int32  `json:"initial_value"`
	Outcome       string `json:"outcome"`
	ExpectedValue *int32 `json:"expected_value,omitempty"`
}

// foundationInt32KVC models only the values captured by the local Foundation
// probe. It is a test fixture for platform behavior, not a production KVC
// policy and not a claim about every Apple platform version.
func foundationInt32KVC(c foundationInt32KVCCase) (int32, string, error) {
	if c.Outcome == "exception" {
		return c.InitialValue, "exception", nil
	}
	switch c.InputKind {
	case "NSNumber-int64":
		value, err := strconv.ParseInt(c.Input, 10, 64)
		if err != nil {
			return 0, "error", err
		}
		return int32(uint32(value)), "value", nil
	case "NSNumber-double":
		value, err := strconv.ParseFloat(c.Input, 64)
		if err != nil {
			return 0, "error", err
		}
		return int32(value), "value", nil
	case "NSNumber-bool":
		if c.Input == "true" {
			return 1, "value", nil
		}
		return 0, "value", nil
	case "NSString":
		value, err := strconv.ParseInt(c.Input, 10, 64)
		if err != nil {
			return 0, "value", nil
		}
		if value > math.MaxInt32 {
			return math.MaxInt32, "value", nil
		}
		if value < math.MinInt32 {
			return math.MinInt32, "value", nil
		}
		return int32(value), "value", nil
	default:
		return 0, "error", nil
	}
}

func TestFoundationInt32KVCFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-foundation-int32-kvc.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture foundationInt32KVCFixture
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Status != "reviewed-platform-bounded-synthetic" || fixture.Platform == "" || fixture.Property != (foundationInt32KVCProperty{Name: "revision", Encoding: "Ti", Width: 4}) || len(fixture.Cases) != 21 {
		t.Fatalf("fixture header=%#v", fixture)
	}
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty case %q", c.Name)
		}
		if c.InputClass == "" || c.InputObjCType == "" {
			t.Fatalf("%s missing boxed input provenance", c.Name)
		}
		seen[c.Name] = true
		got, outcome, err := foundationInt32KVC(c)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if outcome != c.Outcome || got != c.InitialValue && c.ExpectedValue == nil || c.ExpectedValue != nil && got != *c.ExpectedValue {
			t.Errorf("%s got=(%d,%s) want=(%v,%s)", c.Name, got, outcome, c.ExpectedValue, c.Outcome)
		}
	}
}
