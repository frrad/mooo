package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type foundationStringKVCFixture struct {
	Status   string                    `json:"status"`
	Platform string                    `json:"platform"`
	Cases    []foundationStringKVCCase `json:"cases"`
}

type foundationStringKVCCase struct {
	Name          string `json:"name"`
	Input         string `json:"input"`
	InputClass    string `json:"input_class"`
	InputObjCType string `json:"input_objc_type"`
	ExpectedValue int32  `json:"expected_value"`
}

// foundationStringKVC replays only the captured input domain. It deliberately
// does not claim a portable Unicode or numeric-string grammar.
func foundationStringKVC(input string) (int32, bool) {
	switch input {
	case "42", "\t42", " 42", "\u00a042", "\u200342", "\u300042", "٤٢", "４２":
		return 42, true
	case "- 42":
		return -42, true
	case "42suffix":
		return 42, true
	case "\n42", "\r42", "\f42", "\v42":
		return 0, true
	case "999999999999999999999999":
		return 2147483647, true
	case "-999999999999999999999999":
		return -2147483648, true
	default:
		return 0, false
	}
}

func TestFoundationStringKVCFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-foundation-string-kvc.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture foundationStringKVCFixture
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Status != "reviewed-platform-bounded-synthetic" || fixture.Platform == "" || len(fixture.Cases) != 16 {
		t.Fatalf("fixture header=%#v", fixture)
	}
	seen := map[string]bool{}
	for _, c := range fixture.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty case %q", c.Name)
		}
		seen[c.Name] = true
		if c.InputClass == "" || c.InputObjCType != "@" {
			t.Fatalf("%s missing NSString provenance", c.Name)
		}
		got, ok := foundationStringKVC(c.Input)
		if !ok || got != c.ExpectedValue {
			t.Errorf("%s got=%d/%t want=%d", c.Name, got, ok, c.ExpectedValue)
		}
	}
}
