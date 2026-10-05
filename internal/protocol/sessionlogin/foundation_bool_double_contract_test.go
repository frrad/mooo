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

type foundationBoolDoubleFixture struct {
	Status   string                     `json:"status"`
	Platform string                     `json:"platform"`
	Cases    []foundationBoolDoubleCase `json:"cases"`
}

type foundationBoolDoubleCase struct {
	Name          string `json:"name"`
	InputKind     string `json:"input_kind"`
	Input         string `json:"input"`
	InputClass    string `json:"input_class"`
	InputObjCType string `json:"input_objc_type"`
	ExpectedValue int32  `json:"expected_value"`
}

// foundationBoolDoubleKVC replays only the captured macOS Foundation values.
// It is deliberately a named-vector contract, not a generic floating-point or
// string conversion policy.
func foundationBoolDoubleKVC(c foundationBoolDoubleCase) (int32, bool) {
	switch c.InputKind {
	case "NSNumber-bool":
		v, err := strconv.ParseBool(c.Input)
		if err != nil {
			return 0, false
		}
		if v {
			return 1, true
		}
		return 0, true
	case "NSNumber-double":
		var v float64
		var err error
		if c.Input == "-NaN" {
			v = math.Copysign(math.NaN(), -1)
		} else {
			v, err = strconv.ParseFloat(c.Input, 64)
			if err != nil {
				return 0, false
			}
		}
		if math.IsNaN(v) {
			return 0, true
		}
		// Captured Foundation behavior clamps finite/out-of-range values to
		// signed-64 extremes before the Ti low-word conversion.
		const maxInt64AsFloat = 9223372036854775808.0
		if math.IsInf(v, 1) || v >= maxInt64AsFloat {
			return -1, true
		}
		if math.IsInf(v, -1) || v <= -maxInt64AsFloat {
			return 0, true
		}
		return int32(uint32(int64(math.Trunc(v)))), true
	}
	return 0, false
}

func TestFoundationBoolDoubleKVCFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-foundation-bool-double.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture foundationBoolDoubleFixture
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Status != "reviewed-platform-bounded-synthetic" || fixture.Platform == "" || len(fixture.Cases) != 26 {
		t.Fatalf("fixture header=%#v", fixture)
	}
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty case %q", c.Name)
		}
		seen[c.Name] = true
		if c.InputClass == "" || c.InputObjCType == "" {
			t.Fatalf("%s missing input provenance", c.Name)
		}
		got, ok := foundationBoolDoubleKVC(c)
		if !ok || got != c.ExpectedValue {
			t.Errorf("%s got=%d/%t want=%d", c.Name, got, ok, c.ExpectedValue)
		}
	}
}
