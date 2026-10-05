package sessionlogin

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"sort"
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

func receiptSourceProperties(c receiptBodyCase) map[string]any {
	return map[string]any{
		"method":       c.method,
		"packetId":     c.packetID,
		"revision":     c.revision,
		"plusRevision": c.plus,
	}
}

// projectReceiptBody models the source-observed static property removal and
// BLOCKSYNC mapping phase. The input starts with typed inherited/header and
// receipt properties, removes method/packetId, then renames the signed source
// fields before encoding the resulting int32 dictionary as BSON.
func projectReceiptBody(c receiptBodyCase) (map[string]int32, []byte) {
	source := receiptSourceProperties(c)
	delete(source, "method")
	delete(source, "packetId")
	properties := make(map[string]int32)
	if c.kind == "hint" {
		return properties, encodeInt32BSON(properties)
	}
	properties["r"] = source["revision"].(int32)
	properties["pr"] = source["plusRevision"].(int32)
	return properties, encodeInt32BSON(properties)
}

func encodeInt32BSON(values map[string]int32) []byte {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	body := make([]byte, 0, len(values)*10+1)
	for _, key := range keys {
		body = append(body, 0x10)
		body = append(body, key...)
		body = append(body, 0)
		var value [4]byte
		binary.LittleEndian.PutUint32(value[:], uint32(values[key]))
		body = append(body, value[:]...)
	}
	body = append(body, 0)
	out := make([]byte, 4, len(body)+4)
	binary.LittleEndian.PutUint32(out, uint32(len(body)+4))
	return append(out, body...)
}

func TestPushReceiptBodyComposition(t *testing.T) {
	cases := []receiptBodyCase{
		{name: "hint_empty_with_header_fields_removed", kind: "hint", method: "HINT", packetID: 17, expected: map[string]int32{}, expectedBSON: []byte{5, 0, 0, 0, 0}},
		{name: "hint_zero_header_fields_still_empty", kind: "hint", method: "HINT", packetID: 0, expected: map[string]int32{}, expectedBSON: []byte{5, 0, 0, 0, 0}},
		{name: "blocksync_signed_values", kind: "block_sync", method: "BLOCKSYNC", packetID: 9, revision: -1, plus: 2147483647, expected: map[string]int32{"r": -1, "pr": 2147483647}, expectedBSON: []byte{20, 0, 0, 0, 0x10, 'p', 'r', 0, 0xff, 0xff, 0xff, 0x7f, 0x10, 'r', 0, 0xff, 0xff, 0xff, 0xff, 0}},
		{name: "blocksync_zero_values", kind: "block_sync", method: "BLOCKSYNC", packetID: 9, expected: map[string]int32{"r": 0, "pr": 0}, expectedBSON: []byte{20, 0, 0, 0, 0x10, 'p', 'r', 0, 0, 0, 0, 0, 0x10, 'r', 0, 0, 0, 0, 0, 0}},
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

func TestPushReceiptBodyRequiresStaticHeaderRemoval(t *testing.T) {
	input := receiptBodyCase{kind: "hint", method: "HINT", packetID: 17}
	source := receiptSourceProperties(input)
	if source["method"] != input.method || source["packetId"] != input.packetID {
		t.Fatalf("typed source fields=%#v", source)
	}
	projected, body := projectReceiptBody(input)
	if len(projected) != 0 || !bytes.Equal(body, []byte{5, 0, 0, 0, 0}) {
		t.Fatalf("static removal projection=%v bson=%x", projected, body)
	}
	withoutRemoval := receiptSourceProperties(input)
	if _, ok := withoutRemoval["method"]; !ok {
		t.Fatal("negative control did not retain method")
	}
	if _, ok := withoutRemoval["packetId"]; !ok {
		t.Fatal("negative control did not retain packetId")
	}
	if len(withoutRemoval) == len(projected) {
		t.Fatal("omitting static removal was not distinguishable")
	}
}
