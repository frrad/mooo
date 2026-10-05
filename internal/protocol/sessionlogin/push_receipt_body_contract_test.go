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
	expected     map[string]any
	expectedBSON []byte
}

func receiptSourceProperties(c receiptBodyCase) map[string]any {
	properties := map[string]any{"method": c.method, "packetId": c.packetID}
	if c.kind == "block_sync" {
		properties["revision"] = c.revision
		properties["plusRevision"] = c.plus
	}
	return properties
}

// projectReceiptBody mutates the same typed source dictionary that is encoded:
// static header removal and source-to-destination renames are therefore
// observable if either operation is omitted.
func projectReceiptBody(c receiptBodyCase) (map[string]any, []byte) {
	properties := receiptSourceProperties(c)
	delete(properties, "method")
	delete(properties, "packetId")
	if c.kind == "block_sync" {
		properties["r"] = properties["revision"]
		delete(properties, "revision")
		properties["pr"] = properties["plusRevision"]
		delete(properties, "plusRevision")
	}
	return properties, encodeReceiptBSON(properties)
}

func encodeReceiptBSON(values map[string]any) []byte {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	body := make([]byte, 0, len(values)*16+1)
	for _, key := range keys {
		switch value := values[key].(type) {
		case int32:
			body = append(body, 0x10)
			body = append(body, key...)
			body = append(body, 0)
			var encoded [4]byte
			binary.LittleEndian.PutUint32(encoded[:], uint32(value))
			body = append(body, encoded[:]...)
		case string:
			body = append(body, 0x02)
			body = append(body, key...)
			body = append(body, 0)
			var length [4]byte
			binary.LittleEndian.PutUint32(length[:], uint32(len(value)+1))
			body = append(body, length[:]...)
			body = append(body, value...)
			body = append(body, 0)
		case uint32:
			// This uint32 branch exists only so the negative-control mutation
			// produces observable bytes. It is not an assertion about the
			// official NSNumber/BSON support matrix.
			body = append(body, 0x12)
			body = append(body, key...)
			body = append(body, 0)
			var encoded [8]byte
			binary.LittleEndian.PutUint64(encoded[:], uint64(value))
			body = append(body, encoded[:]...)
		default:
			panic("unsupported synthetic BSON value")
		}
	}
	body = append(body, 0)
	out := make([]byte, 4, len(body)+4)
	binary.LittleEndian.PutUint32(out, uint32(len(body)+4))
	return append(out, body...)
}

func TestPushReceiptBodyComposition(t *testing.T) {
	cases := []receiptBodyCase{
		{name: "hint_empty_with_header_fields_removed", kind: "hint", method: "HINT", packetID: 17, expected: map[string]any{}, expectedBSON: []byte{5, 0, 0, 0, 0}},
		{name: "hint_zero_header_fields_still_empty", kind: "hint", method: "HINT", packetID: 0, expected: map[string]any{}, expectedBSON: []byte{5, 0, 0, 0, 0}},
		{name: "blocksync_signed_values", kind: "block_sync", method: "BLOCKSYNC", packetID: 9, revision: -1, plus: 2147483647, expected: map[string]any{"r": int32(-1), "pr": int32(2147483647)}, expectedBSON: []byte{20, 0, 0, 0, 0x10, 'p', 'r', 0, 0xff, 0xff, 0xff, 0x7f, 0x10, 'r', 0, 0xff, 0xff, 0xff, 0xff, 0}},
		{name: "blocksync_zero_values", kind: "block_sync", method: "BLOCKSYNC", packetID: 9, expected: map[string]any{"r": int32(0), "pr": int32(0)}, expectedBSON: []byte{20, 0, 0, 0, 0x10, 'p', 'r', 0, 0, 0, 0, 0, 0x10, 'r', 0, 0, 0, 0, 0, 0}},
	}
	for _, c := range cases {
		got, body := projectReceiptBody(c)
		if !reflect.DeepEqual(got, c.expected) {
			t.Errorf("%s body=%v want %v", c.name, got, c.expected)
		}
		if !bytes.Equal(body, c.expectedBSON) {
			t.Errorf("%s bson=%v want %v", c.name, body, c.expectedBSON)
		}
		if c.kind == "block_sync" {
			if _, ok := got["method"]; ok {
				t.Errorf("%s leaked method", c.name)
			}
			if _, ok := got["packetId"]; ok {
				t.Errorf("%s leaked packetId", c.name)
			}
			if c.revision == -1 && got["r"] != int32(-1) {
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
	if bytes.Equal(encodeReceiptBSON(withoutRemoval), body) {
		t.Fatal("omitting static removal produced the expected empty BSON")
	}
}
