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
	Name                   string   `json:"name"`
	CryptoPresent          bool     `json:"crypto_present"`
	InputPresent           bool     `json:"input_present"`
	InputIdentity          string   `json:"input_identity"`
	InputLength            uint64   `json:"input_length"`
	DecryptResultPresent   bool     `json:"decrypt_result_present"`
	CryptoResultIdentity   string   `json:"crypto_result_identity"`
	ExpectedSupplyPresent  bool     `json:"expected_supply_present"`
	ExpectedSupplyIdentity string   `json:"expected_supply_identity"`
	ExpectedIVOffset       *uint64  `json:"expected_iv_offset"`
	ExpectedIVLength       *uint64  `json:"expected_iv_length"`
	ExpectedCipherOffset   *uint64  `json:"expected_cipher_offset"`
	ExpectedCipherLength   *uint64  `json:"expected_cipher_length"`
	ExpectedTagOffset      *uint64  `json:"expected_tag_offset"`
	ExpectedTagLength      *uint64  `json:"expected_tag_length"`
	ExpectedEffects        []string `json:"expected_effects"`
}

type v2slReceiveProjection struct {
	SupplyPresent              bool
	SupplyIdentity             string
	IVOffset, IVLength         *uint64
	CipherOffset, CipherLength *uint64
	TagOffset, TagLength       *uint64
	Effects                    []string
}

func projectV2SLReceive(c v2slReceiveCase) v2slReceiveProjection {
	if !c.CryptoPresent {
		return v2slReceiveProjection{SupplyPresent: c.InputPresent, SupplyIdentity: c.InputIdentity, Effects: []string{"supply_raw_without_decrypt"}}
	}
	cipherLength := c.InputLength - 28 // Fixtures are bounded to N >= 28; source guard is untraced.
	ivOffset, ivLength := uint64(0), uint64(12)
	cipherOffset, tagLength := uint64(12), uint64(16)
	tagOffset := c.InputLength - tagLength
	supplyIdentity := ""
	if c.DecryptResultPresent {
		supplyIdentity = c.CryptoResultIdentity
	}
	return v2slReceiveProjection{
		SupplyPresent: c.DecryptResultPresent, SupplyIdentity: supplyIdentity,
		IVOffset: &ivOffset, IVLength: &ivLength, CipherOffset: &cipherOffset, CipherLength: &cipherLength,
		TagOffset: &tagOffset, TagLength: &tagLength,
		Effects: []string{"construct_iv_data_length_12", "construct_cipher_data_offset_12", "construct_cipher_data_length_input_minus_28", "construct_tag_data_tail_length_16", "invoke_decrypt_gcm", "supply_decrypt_result"},
	}
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
		p := projectV2SLReceive(c)
		if p.SupplyPresent != c.ExpectedSupplyPresent || p.SupplyIdentity != c.ExpectedSupplyIdentity ||
			!reflect.DeepEqual(p.IVOffset, c.ExpectedIVOffset) || !reflect.DeepEqual(p.IVLength, c.ExpectedIVLength) ||
			!reflect.DeepEqual(p.CipherOffset, c.ExpectedCipherOffset) || !reflect.DeepEqual(p.CipherLength, c.ExpectedCipherLength) ||
			!reflect.DeepEqual(p.TagOffset, c.ExpectedTagOffset) || !reflect.DeepEqual(p.TagLength, c.ExpectedTagLength) ||
			!reflect.DeepEqual(p.Effects, c.ExpectedEffects) {
			t.Errorf("%s projection=%#v want supply=(%v,%q) slices=(%v,%v,%v,%v,%v,%v) effects=%v", c.Name, p, c.ExpectedSupplyPresent, c.ExpectedSupplyIdentity, c.ExpectedIVOffset, c.ExpectedIVLength, c.ExpectedCipherOffset, c.ExpectedCipherLength, c.ExpectedTagOffset, c.ExpectedTagLength, c.ExpectedEffects)
		}
	}
}
