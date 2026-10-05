package sessionlogin

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	Name     string            `json:"name"`
	BSONHex  string            `json:"bson_hex"`
	Expected map[string]string `json:"expected"`
	Outcome  string            `json:"outcome"`
}

// projectInvalidUTF8Dictionary is a bounded model for the observed string
// factory and dictionary boundary. It accepts only the two ASCII values used
// by the synthetic fixtures; it is not the production BSON decoder.
var errInvalidDictionaryKey = errors.New("invalid dictionary key")

func projectInvalidUTF8Dictionary(raw []byte) (map[string]string, error) {
	if len(raw) < 5 || int(binary.LittleEndian.Uint32(raw[:4])) != len(raw) {
		return nil, fmt.Errorf("invalid BSON framing")
	}
	out := make(map[string]string)
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
			return nil, fmt.Errorf("%w: nonnil value %q", errInvalidDictionaryKey, value)
		}
		out[key] = value // later nonnil values replace earlier values
	}
	return nil, fmt.Errorf("missing BSON terminator")
}

func boundedCString(raw []byte) (string, bool) {
	if nul := bytes.IndexByte(raw, 0); nul >= 0 {
		raw = raw[:nul]
	}
	if !utf8.Valid(raw) {
		return "", false
	}
	return string(raw), true
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
	if fixture.Status != "reviewed-platform-bounded-synthetic" || len(fixture.Cases) != 9 {
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
				if !errors.Is(modelErr, errInvalidDictionaryKey) {
					t.Fatalf("error=%v, want invalid dictionary key", modelErr)
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
					t.Errorf("%q=%q want %q", key, got[key], want)
				}
			}
		})
	}
}
