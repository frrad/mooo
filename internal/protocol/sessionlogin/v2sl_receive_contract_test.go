package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type v2slReceiveFixture struct {
	Status string            `json:"status"`
	Cases  []v2slReceiveCase `json:"cases"`
}

type v2slReceiveCase struct {
	Name                 string   `json:"name"`
	CryptoPresent        bool     `json:"crypto_present"`
	InputPresent         bool     `json:"input_present"`
	InputLength          uint64   `json:"input_length"`
	DecryptResultPresent bool     `json:"decrypt_result_present"`
	ExpectedCipherLength *uint64  `json:"expected_cipher_length"`
	ExpectedEffects      []string `json:"expected_effects"`
}

func projectV2SLReceive(c v2slReceiveCase) []string {
	if !c.CryptoPresent {
		return []string{"supply_raw_without_decrypt"}
	}
	return []string{"construct_iv_data_length_12", "construct_cipher_data_offset_12", "construct_cipher_data_length_input_minus_28", "construct_tag_data_tail_length_16", "invoke_decrypt_gcm", "supply_decrypt_result"}
}

func TestV2SLReceiveContract(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-v2sl-receive.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f v2slReceiveFixture
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 4 {
		t.Fatalf("fixture header=%#v", f)
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty case %q", c.Name)
		}
		seen[c.Name] = true
		if c.CryptoPresent && (!c.InputPresent || c.InputLength < 28) {
			t.Fatalf("secure case must have at least IV+tag bytes: %s", c.Name)
		}
		if c.ExpectedCipherLength != nil && *c.ExpectedCipherLength != c.InputLength-28 {
			t.Errorf("%s cipher length=%d want=%d", c.Name, *c.ExpectedCipherLength, c.InputLength-28)
		}
		if got, want := projectV2SLReceive(c), c.ExpectedEffects; !reflect.DeepEqual(got, want) {
			t.Errorf("%s effects=%v want=%v", c.Name, got, want)
		}
	}
}
