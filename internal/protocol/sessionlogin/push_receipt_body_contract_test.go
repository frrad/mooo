package sessionlogin

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"
)

type receiptBodyCase struct {
	name         string
	kind         string
	method       string
	packetID     uint32
	revision     int32
	plus         int32
	expected     map[string]int32
	expectedBSON []byte
}

// projectReceiptBody models the source-observed static property removal and
// BLOCKSYNC mapping phase. It intentionally leaves BSON element order to the
// encoder; the source receipt proves the keys/types, while the empty HINT
// document is an exact five-byte vector.
func projectReceiptBody(c receiptBodyCase) (map[string]int32, []byte) {
	if c.kind == "hint" {
		return map[string]int32{}, []byte{5, 0, 0, 0, 0}
	}
	return map[string]int32{"r": c.revision, "pr": c.plus}, nil
}

func TestPushReceiptBodyComposition(t *testing.T) {
	cases := []receiptBodyCase{
		{name: "hint_empty_with_header_fields_removed", kind: "hint", method: "HINT", packetID: 17, expected: map[string]int32{}, expectedBSON: []byte{5, 0, 0, 0, 0}},
		{name: "hint_zero_header_fields_still_empty", kind: "hint", method: "HINT", packetID: 0, expected: map[string]int32{}, expectedBSON: []byte{5, 0, 0, 0, 0}},
		{name: "blocksync_signed_values", kind: "block_sync", method: "BLOCKSYNC", packetID: 9, revision: -1, plus: 2147483647, expected: map[string]int32{"r": -1, "pr": 2147483647}},
		{name: "blocksync_zero_values", kind: "block_sync", method: "BLOCKSYNC", packetID: 9, expected: map[string]int32{"r": 0, "pr": 0}},
	}
	for _, c := range cases {
		got, body := projectReceiptBody(c)
		if !reflect.DeepEqual(got, c.expected) {
			t.Errorf("%s body=%v want %v", c.name, got, c.expected)
		}
		if c.expectedBSON != nil && !bytes.Equal(body, c.expectedBSON) {
			t.Errorf("%s bson=%v want %v", c.name, body, c.expectedBSON)
		}
		if c.kind == "block_sync" {
			if _, ok := got["method"]; ok {
				t.Errorf("%s leaked method", c.name)
			}
			if _, ok := got["packetId"]; ok {
				t.Errorf("%s leaked packetId", c.name)
			}
			for _, key := range []string{"r", "pr"} {
				if got[key] != c.expected[key] {
					t.Errorf("%s key %s mismatch", c.name, key)
				}
			}
			var b [4]byte
			binary.LittleEndian.PutUint32(b[:], uint32(got["r"]))
			if c.revision == -1 && !bytes.Equal(b[:], []byte{0xff, 0xff, 0xff, 0xff}) {
				t.Errorf("%s signed revision width", c.name)
			}
		}
	}
}
