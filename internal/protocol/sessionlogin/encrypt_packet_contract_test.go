package sessionlogin

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type encryptPacketFixture struct {
	Status string              `json:"status"`
	Cases  []encryptPacketCase `json:"cases"`
}

type encryptPacketCase struct {
	Name                   string   `json:"name"`
	CipherPresent          bool     `json:"cipher_present"`
	MutableDataPresent     bool     `json:"mutable_data_present"`
	InputPresent           bool     `json:"input_present"`
	InputIdentity          string   `json:"input_identity"`
	EncryptedResult        string   `json:"encrypted_result"` // bytes, empty, or nil
	EncryptedBytes         string   `json:"encrypted_bytes"`
	EncryptedLength        uint64   `json:"encrypted_length"`
	ExpectedOutputHex      string   `json:"expected_output_hex"`
	ExpectedOutputIdentity string   `json:"expected_output_identity"`
	ExpectedOutputStatus   string   `json:"expected_output_status"`
	ExpectedEffects        []string `json:"expected_effects"`
}

type encryptPacketProjection struct {
	OutputHex      string
	OutputIdentity string
	OutputStatus   string
	Effects        []string
}

func projectEncryptPacket(c encryptPacketCase) encryptPacketProjection {
	if !c.CipherPresent {
		if !c.InputPresent {
			return encryptPacketProjection{OutputStatus: "nil_input", Effects: []string{"retain_input", "return_input_identity"}}
		}
		return encryptPacketProjection{OutputIdentity: c.InputIdentity, OutputStatus: "input_identity", Effects: []string{"retain_input", "return_input_identity"}}
	}
	effects := []string{"allocate_mutable_data", "invoke_crypto_encrypt", "read_result_length_uint32", "append_length_prefix_little_endian"}
	if !c.MutableDataPresent {
		return encryptPacketProjection{OutputStatus: "nil_mutable_container", Effects: append(effects, "append_crypto_result")}
	}
	if c.EncryptedResult == "nil" {
		return encryptPacketProjection{OutputHex: "00000000", OutputStatus: "framed_nil_crypto_result_platform_probe", Effects: append(effects, "append_crypto_result_nil")}
	}
	effects = append(effects, "append_crypto_result")
	length := uint32(c.EncryptedLength)
	prefix := make([]byte, 4)
	binary.LittleEndian.PutUint32(prefix, length)
	result, _ := hex.DecodeString(c.EncryptedBytes)
	return encryptPacketProjection{OutputHex: hex.EncodeToString(append(prefix, result...)), OutputStatus: "framed_bytes", Effects: effects}
}

func TestEncryptPacketContract(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-encrypt-packet.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f encryptPacketFixture
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 7 {
		t.Fatalf("fixture header = %#v", f)
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty case %q", c.Name)
		}
		seen[c.Name] = true
		got := projectEncryptPacket(c)
		want := encryptPacketProjection{OutputHex: c.ExpectedOutputHex, OutputIdentity: c.ExpectedOutputIdentity, OutputStatus: c.ExpectedOutputStatus, Effects: c.ExpectedEffects}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", c.Name, got, want)
		}
	}
}
