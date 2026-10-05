package sessionlogin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type packetDataDecoderFixture struct {
	Status string                  `json:"status"`
	Cases  []packetDataDecoderCase `json:"cases"`
	BSON   bsonDecoderFixture      `json:"bson_decoder"`
}

type packetDataDecoderCase struct {
	Name             string   `json:"name"`
	SuperInitOK      bool     `json:"super_init_ok"`
	HeaderInitOK     bool     `json:"header_init_ok"`
	InputLength      uint32   `json:"input_length"`
	HeaderBodyLength uint32   `json:"header_body_length"`
	DecoderResult    string   `json:"decoder_result"`
	ExpectedReturned bool     `json:"expected_returned"`
	ExpectedBody     string   `json:"expected_body"`
	ExpectedSliceLen uint32   `json:"expected_slice_length"`
	ExpectedEffects  []string `json:"expected_effects"`
}

type bsonDecoderFixture struct {
	Status string            `json:"status"`
	Cases  []bsonDecoderCase `json:"cases"`
}

type bsonDecoderCase struct {
	Name                     string   `json:"name"`
	Elements                 []string `json:"elements"`
	TerminatorPresent        bool     `json:"terminator_present"`
	TrailingBytes            bool     `json:"trailing_bytes"`
	UnknownTypeAfterElements bool     `json:"unknown_type_after_elements"`
	ExpectedDictionary       []string `json:"expected_dictionary"`
	ExpectedStop             string   `json:"expected_stop"`
}

func projectPacketDataDecoder(c packetDataDecoderCase) (bool, string, uint32, []string) {
	if !c.SuperInitOK {
		return false, "", 0, []string{"super_init_returns_nil"}
	}
	effects := []string{"init_header_from_first_22_bytes", "store_header"}
	if !c.HeaderInitOK {
		return true, "", 0, append(effects, "header_body_length_reads_nil_as_zero")
	}
	if c.HeaderBodyLength == 0 {
		return true, "", 0, append(effects, "body_decode_skipped_zero_header_length")
	}
	// The official constructor uses input.length - 22 for the slice. The
	// header's bodyLength only gates this branch; it is not the slice limit.
	sliceLen := c.InputLength - 22
	effects = append(effects, "slice_body_from_offset_22", "decode_dictionary_with_bson_data")
	if c.DecoderResult == "nil" {
		effects = append(effects, "store_body_decoder_nil")
		return true, "", sliceLen, effects
	}
	effects = append(effects, "store_body_decoder_result")
	return true, c.DecoderResult, sliceLen, effects
}

func TestPacketDataDecoderContract(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-packet-data-decoder.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f packetDataDecoderFixture
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
		returned, packetBody, sliceLen, effects := projectPacketDataDecoder(c)
		if returned != c.ExpectedReturned || packetBody != c.ExpectedBody || sliceLen != c.ExpectedSliceLen || !reflect.DeepEqual(effects, c.ExpectedEffects) {
			t.Errorf("%s result=(%v,%q,%d,%v)", c.Name, returned, packetBody, sliceLen, effects)
		}
	}
}

func projectBSONDecoder(c bsonDecoderCase) ([]string, string) {
	// The observed loop inserts decoded elements until the zero-type BSON
	// terminator or an unknown type makes the cursor helper return zero.
	// Trailing bytes after the terminator are not inspected by this loop.
	if c.UnknownTypeAfterElements {
		return append([]string{}, c.Elements...), "unknown_type_stops_with_partial_dictionary"
	}
	if c.TerminatorPresent {
		return append([]string{}, c.Elements...), "terminator_stops_element_loop"
	}
	return append([]string{}, c.Elements...), "unterminated_input_behavior_unresolved"
}

func TestBSONDecoderObservedContract(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-packet-data-decoder.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f packetDataDecoderFixture
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	if f.BSON.Status != "reviewed-static-unexecuted-runtime" || len(f.BSON.Cases) != 4 {
		t.Fatalf("fixture bson header=%#v", f.BSON)
	}
	seen := map[string]bool{}
	for _, c := range f.BSON.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty BSON case %q", c.Name)
		}
		seen[c.Name] = true
		got, stop := projectBSONDecoder(c)
		if !reflect.DeepEqual(got, c.ExpectedDictionary) || stop != c.ExpectedStop {
			t.Errorf("%s result=(%v,%q)", c.Name, got, stop)
		}
	}
}
