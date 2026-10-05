package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type packetConstructorFixture struct {
	Status string                  `json:"status"`
	Cases  []packetConstructorCase `json:"cases"`
}
type packetConstructorCase struct {
	Name                 string        `json:"name"`
	SuperInitOK          bool          `json:"super_init_ok"`
	PacketID             uint32        `json:"packet_id"`
	Method               string        `json:"method"`
	BodyIdentity         string        `json:"body_identity"`
	BodyPresent          bool          `json:"body_present"`
	HeaderInitOK         bool          `json:"header_init_ok"`
	ExpectedReturned     bool          `json:"expected_returned"`
	ExpectedBodyIdentity string        `json:"expected_body_identity"`
	ExpectedHeader       *packetHeader `json:"expected_header"`
	ExpectedEffects      []string      `json:"expected_effects"`
}
type packetHeader struct {
	PacketID   uint32 `json:"packet_id"`
	StatusCode uint16 `json:"status_code"`
	Method     string `json:"method"`
	BodyType   uint8  `json:"body_type"`
	BodyLength uint32 `json:"body_length"`
}

func projectPacketConstructor(c packetConstructorCase) (bool, string, *packetHeader, []string) {
	if !c.SuperInitOK {
		return false, "", nil, []string{"super_init_returns_nil"}
	}
	effects := []string{"store_body", "init_header_with_defaults"}
	if !c.BodyPresent {
		effects[0] = "store_body_nil"
	}
	if !c.HeaderInitOK {
		effects = append(effects, "store_header_nil")
		return true, c.BodyIdentity, nil, effects
	}
	effects = append(effects, "store_header")
	return true, c.BodyIdentity, &packetHeader{PacketID: c.PacketID, Method: c.Method}, effects
}
func TestPacketConstructorContract(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-packet-constructor.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f packetConstructorFixture
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 5 {
		t.Fatalf("fixture header=%#v", f)
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty case %q", c.Name)
		}
		seen[c.Name] = true
		returned, bodyIdentity, header, effects := projectPacketConstructor(c)
		if returned != c.ExpectedReturned || bodyIdentity != c.ExpectedBodyIdentity || !reflect.DeepEqual(header, c.ExpectedHeader) || !reflect.DeepEqual(effects, c.ExpectedEffects) {
			t.Errorf("%s result=(%v,%q,%#v,%v)", c.Name, returned, bodyIdentity, header, effects)
		}
	}
}
