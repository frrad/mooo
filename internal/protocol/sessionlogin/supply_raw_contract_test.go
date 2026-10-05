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

type supplyRawFrame struct {
	HeaderInitPresent bool   `json:"header_init_present"`
	BodyLength        uint32 `json:"body_length"`
	PacketInitPresent bool   `json:"packet_init_present"`
}

type supplyRawCase struct {
	Name                        string           `json:"name"`
	InitialBufferLength         uint64           `json:"initial_buffer_length"`
	AppendLength                uint64           `json:"append_length"`
	CurrentHeaderPresent        bool             `json:"current_header_present"`
	HeaderInitPresent           bool             `json:"header_init_present"`
	BodyLength                  uint32           `json:"body_length"`
	PacketInitPresent           bool             `json:"packet_init_present"`
	HeaderDelegatePresent       bool             `json:"header_delegate_present"`
	PacketDelegatePresent       bool             `json:"packet_delegate_present"`
	Frames                      []supplyRawFrame `json:"frames"`
	ExpectedReturn              uint64           `json:"expected_return"`
	ExpectedRemaining           uint64           `json:"expected_remaining"`
	ExpectedPacketDataPresent   bool             `json:"expected_packet_data_present"`
	ExpectedPacketDataByFrame   []bool           `json:"expected_packet_data_by_frame"`
	ExpectedCurrentHeader       bool             `json:"expected_current_header"`
	ExpectedFrameInputExhausted bool             `json:"expected_frame_input_exhausted"`
	ExpectedEffects             []string         `json:"expected_effects"`
}

type supplyRawProjection struct {
	Return              uint64
	Remaining           uint64
	PacketDataPresent   bool
	PacketDataByFrame   []bool
	CurrentHeader       bool
	FrameInputExhausted bool
	Effects             []string
}

func (c supplyRawCase) frame(index int) (supplyRawFrame, bool) {
	if len(c.Frames) != 0 {
		if index >= len(c.Frames) {
			return supplyRawFrame{}, false
		}
		return c.Frames[index], true
	}
	if index != 0 {
		return supplyRawFrame{}, false
	}
	return supplyRawFrame{HeaderInitPresent: c.HeaderInitPresent, BodyLength: c.BodyLength, PacketInitPresent: c.PacketInitPresent}, true
}

func projectSupplyRaw(c supplyRawCase) supplyRawProjection {
	buffer := c.InitialBufferLength + c.AppendLength
	effects := []string{"append_data"}
	packetDataByFrame := make([]bool, 0, len(c.Frames))
	frameIndex := 0
	currentHeader := c.CurrentHeaderPresent
	for {
		if buffer < 22 {
			return supplyRawProjection{Remaining: buffer, PacketDataPresent: lastPacketData(packetDataByFrame), PacketDataByFrame: packetDataByFrame, CurrentHeader: currentHeader, Effects: effects}
		}
		frame, available := c.frame(frameIndex)
		if !available {
			effects = append(effects, "frame_input_exhausted")
			return supplyRawProjection{Remaining: buffer, PacketDataPresent: lastPacketData(packetDataByFrame), PacketDataByFrame: packetDataByFrame, CurrentHeader: currentHeader, FrameInputExhausted: true, Effects: effects}
		}
		bodyLength := frame.BodyLength
		if !currentHeader {
			effects = append(effects, "init_header_from_buffer", "set_current_header")
			currentHeader = frame.HeaderInitPresent
			if c.HeaderDelegatePresent {
				effects = append(effects, "produce_packet_header")
			}
			if !frame.HeaderInitPresent {
				bodyLength = 0
			}
		}
		required := uint64(bodyLength) + 22
		if buffer < required {
			return supplyRawProjection{Return: required - buffer, Remaining: buffer, PacketDataPresent: lastPacketData(packetDataByFrame), PacketDataByFrame: packetDataByFrame, CurrentHeader: currentHeader, Effects: effects}
		}
		effects = append(effects, "clear_current_header", "init_packet_data", "consume_packet_bytes")
		currentHeader = false
		packetDataPresent := frame.PacketInitPresent
		packetDataByFrame = append(packetDataByFrame, packetDataPresent)
		buffer -= required
		if c.PacketDelegatePresent {
			effects = append(effects, "produce_packet")
		}
		if buffer == 0 {
			return supplyRawProjection{Remaining: 0, PacketDataPresent: packetDataPresent, PacketDataByFrame: packetDataByFrame, CurrentHeader: currentHeader, Effects: effects}
		}
		frameIndex++
	}
}

func lastPacketData(values []bool) bool {
	if len(values) == 0 {
		return false
	}
	return values[len(values)-1]
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
	if f.Status != "reviewed-static-unexecuted-runtime" || len(f.Cases) != 8 {
		t.Fatalf("fixture header=%#v", f)
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty case %q", c.Name)
		}
		seen[c.Name] = true
		got := projectSupplyRaw(c)
		if got.Return != c.ExpectedReturn || got.Remaining != c.ExpectedRemaining || got.PacketDataPresent != c.ExpectedPacketDataPresent || !reflect.DeepEqual(got.PacketDataByFrame, c.ExpectedPacketDataByFrame) || got.CurrentHeader != c.ExpectedCurrentHeader || got.FrameInputExhausted != c.ExpectedFrameInputExhausted || !reflect.DeepEqual(got.Effects, c.ExpectedEffects) {
			t.Errorf("%s projection=%#v want return=%d remaining=%d packet_data=%v by_frame=%v current_header=%v frame_input_exhausted=%v effects=%v", c.Name, got, c.ExpectedReturn, c.ExpectedRemaining, c.ExpectedPacketDataPresent, c.ExpectedPacketDataByFrame, c.ExpectedCurrentHeader, c.ExpectedFrameInputExhausted, c.ExpectedEffects)
		}
	}
}
