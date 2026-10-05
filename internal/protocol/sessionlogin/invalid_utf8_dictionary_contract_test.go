package sessionlogin

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"
)

type invalidUTF8DictionaryFixture struct {
	Status string                      `json:"status"`
	Cases  []invalidUTF8DictionaryCase `json:"cases"`
}

type invalidUTF8DictionaryCase struct {
	Name     string         `json:"name"`
	BSONHex  string         `json:"bson_hex"`
	Expected map[string]int `json:"expected"`
	Outcome  string         `json:"outcome"`
}

// projectInvalidUTF8Dictionary is a bounded model for the observed string
// factory and dictionary boundary. It accepts only the two ASCII values used
// by the synthetic fixtures; it is not the production BSON decoder.
func projectInvalidUTF8Dictionary(raw []byte) (map[string]int, error) {
	if len(raw) < 5 || int(binary.LittleEndian.Uint32(raw[:4])) != len(raw) {
		return nil, fmt.Errorf("invalid BSON framing")
	}
	out := make(map[string]int)
	for pos := 4; pos < len(raw); {
		if raw[pos] == 0 {
			if pos != len(raw)-1 {
				return nil, fmt.Errorf("trailing bytes")
			}
			return out, nil
		}
		if raw[pos] != 0x02 {
			return nil, fmt.Errorf("unsupported BSON type 0x%02x", raw[pos])
		}
		pos++
		keyStart := pos
		for pos < len(raw) && raw[pos] != 0 {
			pos++
		}
		if pos >= len(raw) {
			return nil, fmt.Errorf("unterminated key")
		}
		keyBytes := raw[keyStart:pos]
		pos++
		if pos+4 > len(raw) {
			return nil, fmt.Errorf("truncated string length")
		}
		length := int(binary.LittleEndian.Uint32(raw[pos : pos+4]))
		pos += 4
		if length < 1 || pos+length > len(raw) || raw[pos+length-1] != 0 {
			return nil, fmt.Errorf("invalid string payload")
		}
		valueBytes := raw[pos : pos+length-1]
		pos += length

		key, keyOK := boundedCString(keyBytes)
		value, valueOK := boundedCString(valueBytes)
		if !valueOK {
			// The source helper skips a nil NSString value and continues.
			continue
		}
		if !keyOK {
			return nil, fmt.Errorf("dictionary insertion with non-nil value and nil key")
		}
		parsed, ok := boundedFixtureInteger(value)
		if !ok {
			return nil, fmt.Errorf("unsupported captured value %q", value)
		}
		out[key] = parsed // later nonnil values replace earlier values
	}
	return nil, fmt.Errorf("missing BSON terminator")
}

func boundedCString(raw []byte) (string, bool) {
	if len(raw) > 0 && raw[len(raw)-1] == 0 {
		raw = raw[:len(raw)-1]
	}
	if !utf8.Valid(raw) {
		return "", false
	}
	return string(raw), true
}

func boundedFixtureInteger(value string) (int, bool) {
	switch value {
	case "7":
		return 7, true
	case "42":
		return 42, true
	default:
		return 0, false
	}
}

func TestInvalidUTF8DictionaryFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-invalid-utf8-dictionary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture invalidUTF8DictionaryFixture
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Status != "reviewed-platform-bounded-synthetic" || len(fixture.Cases) != 5 {
		t.Fatalf("fixture header=%#v", fixture)
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			raw, err := hex.DecodeString(tc.BSONHex)
			if err != nil {
				t.Fatal(err)
			}
			got, modelErr := projectInvalidUTF8Dictionary(raw)
			if tc.Outcome == "error" {
				if modelErr == nil {
					t.Fatalf("model succeeded with %#v, want explicit insertion error", got)
				}
				return
			}
			if modelErr != nil {
				t.Fatalf("model error=%v", modelErr)
			}
			if len(got) != len(tc.Expected) {
				t.Fatalf("got=%#v want=%#v", got, tc.Expected)
			}
			for key, want := range tc.Expected {
				if got[key] != want {
					t.Errorf("%q=%d want %d", key, got[key], want)
				}
			}
		})
	}
}
