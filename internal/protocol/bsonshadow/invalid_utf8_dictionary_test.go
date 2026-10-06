package bsonshadow

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The production receive path decodes with mongo-driver, which differs from
// the official client on six of the ten invalid-UTF-8 dictionary cases. This
// pins those differences and checks that the runtime shadow reports each one
// (and nothing for the four cases where the decoders agree).
func TestCompareReportsInvalidUTF8DictionaryDifferences(t *testing.T) {
	wantKinds := map[string][]Kind{
		"invalid_value_then_later":         {KindOfficialDroppedInvalidUTF8},
		"duplicate_invalid_then_valid":     nil,
		"duplicate_valid_then_invalid":     {KindValueDiffers},
		"duplicate_valid_then_valid":       nil,
		"invalid_key_invalid_value":        {KindOfficialDroppedInvalidUTF8},
		"invalid_key_valid_value":          {KindOfficialRejects},
		"valid_empty_value":                nil,
		"valid_empty_key":                  nil,
		"first_nul_truncates_invalid_tail": {KindStringNulTruncated},
		"invalid_key_empty_value":          {KindOfficialRejects},
	}

	data, err := os.ReadFile(filepath.Join("..", "..", "..", "research", "fixtures", "reconnect", "rc-q5-invalid-utf8-dictionary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name    string `json:"name"`
			BSONHex string `json:"bson_hex"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) != len(wantKinds) {
		t.Fatalf("fixture has %d cases, want %d", len(fixture.Cases), len(wantKinds))
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			want, ok := wantKinds[tc.Name]
			if !ok {
				t.Fatalf("no expectation for fixture case %q", tc.Name)
			}
			raw, err := hex.DecodeString(tc.BSONHex)
			if err != nil {
				t.Fatal(err)
			}
			report := Compare(raw)
			var got []Kind
			for _, discrepancy := range report.Discrepancies {
				got = append(got, discrepancy.Kind)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Compare() kinds = %v, want %v (discrepancies %+v)", got, want, report.Discrepancies)
			}
		})
	}
}
