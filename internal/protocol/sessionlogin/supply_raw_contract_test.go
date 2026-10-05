package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type supplyRawFixture struct {
	Status string          `json:"status"`
	Cases  []supplyRawCase `json:"cases"`
}

type supplyRawCase struct {
	Name                      string   `json:"name"`
	InitialBufferLength       uint64   `json:"initial_buffer_length"`
	AppendLength              uint64   `json:"append_length"`
	CurrentHeaderPresent      bool     `json:"current_header_present"`
	HeaderInitPresent         bool     `json:"header_init_present"`
	BodyLength                uint64   `json:"body_length"`
	PacketInitPresent         bool     `json:"packet_init_present"`
	HeaderDelegatePresent     bool     `json:"header_delegate_present"`
	PacketDelegatePresent     bool     `json:"packet_delegate_present"`
	ExpectedReturn            uint64   `json:"expected_return"`
	ExpectedRemaining         uint64   `json:"expected_remaining"`
	ExpectedPacketDataPresent bool     `json:"expected_packet_data_present"`
	ExpectedEffects           []string `json:"expected_effects"`
}

type supplyRawProjection struct {
	Return            uint64
	Remaining         uint64
	PacketDataPresent bool
	Effects           []string
}

func projectSupplyRaw(c supplyRawCase) supplyRawProjection {
	buffer := c.InitialBufferLength + c.AppendLength
	effects := []string{"append_data"}
	for {
		if buffer < 22 {
			return supplyRawProjection{Effects: effects}
		}
		bodyLength := c.BodyLength
		if !c.CurrentHeaderPresent {
			effects = append(effects, "init_header_from_buffer", "set_current_header")
			if !c.HeaderInitPresent {
				bodyLength = 0
			}
		}
		required := bodyLength + 22
		if buffer < required {
			return supplyRawProjection{Return: required - buffer, Remaining: buffer, Effects: effects}
		}
		effects = append(effects, "init_packet_data", "consume_packet_bytes")
		packetDataPresent := c.PacketInitPresent
		buffer -= required
		if c.HeaderDelegatePresent {
			effects = append(effects, "produce_packet_header")
		}
		if c.PacketDelegatePresent {
			effects = append(effects, "produce_packet")
		}
		if buffer == 0 {
			return supplyRawProjection{Remaining: 0, PacketDataPresent: packetDataPresent, Effects: effects}
		}
		// The source clears currentPacketHeader before the packet callbacks;
		// subsequent buffered bytes begin a fresh header parse.
		c.CurrentHeaderPresent = false
		c.HeaderInitPresent = true
	}
}

func TestSupplyRawContract(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-supply-raw.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f supplyRawFixture
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 7 {
		t.Fatalf("fixture header=%#v", f)
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty case %q", c.Name)
		}
		seen[c.Name] = true
		got := projectSupplyRaw(c)
		if got.Return != c.ExpectedReturn || got.Remaining != c.ExpectedRemaining || got.PacketDataPresent != c.ExpectedPacketDataPresent || !reflect.DeepEqual(got.Effects, c.ExpectedEffects) {
			t.Errorf("%s projection=%#v want return=%d remaining=%d packet_data=%v effects=%v", c.Name, got, c.ExpectedReturn, c.ExpectedRemaining, c.ExpectedPacketDataPresent, c.ExpectedEffects)
		}
	}
}
