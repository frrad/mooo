package loco

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type invalidUTF8DictionaryFixture struct {
	Cases []struct {
		Name     string            `json:"name"`
		BSONHex  string            `json:"bson_hex"`
		Expected map[string]string `json:"expected"`
		Outcome  string            `json:"outcome"`
	} `json:"cases"`
}

// The observed decoder must reproduce the official client's handling of
// invalid UTF-8 keys and values, duplicate keys, and embedded NUL bytes.
func TestDecodeObservedBSONInvalidUTF8DictionaryFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "research", "fixtures", "reconnect", "rc-q5-invalid-utf8-dictionary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture invalidUTF8DictionaryFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != 10 {
		t.Fatalf("fixture has %d cases, want 10", len(fixture.Cases))
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			raw, err := hex.DecodeString(tc.BSONHex)
			if err != nil {
				t.Fatal(err)
			}
			result, err := DecodeObservedBSON(raw, BSONDecodeOptions{})
			switch tc.Outcome {
			case "error":
				if err == nil {
					t.Fatalf("DecodeObservedBSON() = %v, want error", result.Document)
				}
				return
			case "ok":
				if err != nil {
					t.Fatalf("DecodeObservedBSON() error = %v", err)
				}
			default:
				t.Fatalf("unknown outcome %q", tc.Outcome)
			}
			got := map[string]string{}
			for key, value := range result.Document {
				text, ok := value.(string)
				if !ok {
					t.Fatalf("field %q has type %T, want string", key, value)
				}
				got[key] = text
			}
			if !reflect.DeepEqual(got, tc.Expected) {
				t.Fatalf("DecodeObservedBSON() = %q, want %q", got, tc.Expected)
			}
		})
	}
}
