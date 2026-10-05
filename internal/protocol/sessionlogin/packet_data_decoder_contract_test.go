package sessionlogin

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	BSONHex                  string   `json:"bson_hex"`
	ExpectedCursorSteps      []int    `json:"expected_cursor_steps"`
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

// projectBSONBytes is a bounded test model for the source-observed cases. It
// intentionally starts at byte four and never uses the BSON document-length
// prefix: the reviewed IMP passes a raw NSData pointer to its cursor helper,
// which has no NSData length argument. This is not the production parser.
func projectBSONBytes(raw []byte) ([]string, []int, string, error) {
	if len(raw) < 4 {
		return nil, nil, "malformed_input_unresolved", nil
	}
	pos := 4
	entries := []string{}
	steps := []int{}
	for pos < len(raw) {
		start := pos
		typ := raw[pos]
		if typ == 0 {
			return entries, append(steps, 0), "terminator_stops_element_loop", nil
		}
		pos++
		keyEnd := bytes.IndexByte(raw[pos:], 0)
		if keyEnd < 0 {
			return entries, steps, "malformed_input_unresolved", nil
		}
		key := string(raw[pos : pos+keyEnd])
		pos += keyEnd + 1
		switch typ {
		case 0x10: // BSON int32: the cursor advances over four payload bytes.
			if len(raw)-pos < 4 {
				return entries, steps, "malformed_input_unresolved", nil
			}
			value := int32(binary.LittleEndian.Uint32(raw[pos : pos+4]))
			entries = append(entries, fmt.Sprintf("%s=%d:int32", key, value))
			pos += 4
		case 0x12: // BSON int64: the cursor advances over eight payload bytes.
			if len(raw)-pos < 8 {
				return entries, steps, "malformed_input_unresolved", nil
			}
			value := int64(binary.LittleEndian.Uint64(raw[pos : pos+8]))
			entries = append(entries, fmt.Sprintf("%s=%d:int64", key, value))
			pos += 8
		case 0x02: // BSON string: four-byte byte length, bytes, then NUL.
			if len(raw)-pos < 4 {
				return entries, steps, "malformed_input_unresolved", nil
			}
			length := int(binary.LittleEndian.Uint32(raw[pos : pos+4]))
			pos += 4
			if length < 1 || length > len(raw)-pos {
				return entries, steps, "malformed_input_unresolved", nil
			}
			if raw[pos+length-1] != 0 {
				return entries, steps, "malformed_input_unresolved", nil
			}
			entries = append(entries, fmt.Sprintf("%s=%s:string", key, string(raw[pos:pos+length-1])))
			pos += length
		default:
			return entries, steps, "unknown_type_stops_with_partial_dictionary", nil
		}
		steps = append(steps, pos-start)
	}
	return entries, steps, "unterminated_input_behavior_unresolved", nil
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
		if c.BSONHex == "" {
			t.Errorf("%s missing bounded BSON hex vector", c.Name)
			continue
		}
		raw, err := hex.DecodeString(c.BSONHex)
		if err != nil {
			t.Fatal(err)
		}
		entries, steps, byteStop, err := projectBSONBytes(raw)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(entries, c.ExpectedDictionary) || !reflect.DeepEqual(steps, c.ExpectedCursorSteps) || byteStop != c.ExpectedStop {
			t.Errorf("%s bytes result=(%v,%v,%q)", c.Name, entries, steps, byteStop)
		}
		// The source cursor ignores the document-length prefix. Mutating it
		// must not change the bounded valid/partial projection.
		if len(raw) >= 4 {
			mutated := append([]byte(nil), raw...)
			binary.LittleEndian.PutUint32(mutated[:4], 1)
			mutEntries, mutSteps, mutStop, err := projectBSONBytes(mutated)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(mutEntries, entries) || !reflect.DeepEqual(mutSteps, steps) || mutStop != byteStop {
				t.Errorf("%s declared length unexpectedly affected decode: got=(%v,%v,%q) want=(%v,%v,%q)", c.Name, mutEntries, mutSteps, mutStop, entries, steps, byteStop)
			}
		}
	}
}
