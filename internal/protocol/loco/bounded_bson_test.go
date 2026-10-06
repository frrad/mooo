package loco

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
)

func testBSONDocument(elements ...[]byte) []byte {
	body := []byte{}
	for _, element := range elements {
		body = append(body, element...)
	}
	body = append(body, 0)
	out := make([]byte, 4, 4+len(body))
	binary.LittleEndian.PutUint32(out, uint32(len(out)+len(body)))
	return append(out, body...)
}

func testBSONElement(typ byte, key string, payload []byte) []byte {
	out := []byte{typ}
	out = append(out, key...)
	out = append(out, 0)
	return append(out, payload...)
}

func TestDecodeObservedBSONSupportedAndSkipTypes(t *testing.T) {
	// The declared document length is intentionally unrelated to the cursor;
	// source parsing starts at byte four. Null leaves the earlier duplicate.
	data := []byte{
		1, 0, 0, 0,
		0x10, 'r', 0, 1, 0, 0, 0,
		0x0a, 'r', 0,
		0x08, 'b', 0, 2,
		0x00,
	}
	got, err := DecodeObservedBSON(data, BSONDecodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Partial || !reflect.DeepEqual(got.Document, map[string]any{"r": int32(1), "b": true}) {
		t.Fatalf("result=%#v", got)
	}
}

func TestDecodeObservedBSONUnknownReturnsPartial(t *testing.T) {
	data := []byte{5, 0, 0, 0, 0x10, 'r', 0, 7, 0, 0, 0, 0x7f, 'x', 0, 1, 0, 0, 0, 0}
	got, err := DecodeObservedBSON(data, BSONDecodeOptions{})
	if err != nil || !got.Partial || got.UnknownType != 0x7f || got.Document["r"] != int32(7) {
		t.Fatalf("result=%#v err=%v", got, err)
	}
}

func TestDecodeObservedBSONRecursiveContainersAndBounds(t *testing.T) {
	data := []byte{5, 0, 0, 0, 0x03, 'n', 0, 16, 0, 0, 0, 0x12, 'i', 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	got, err := DecodeObservedBSON(data, BSONDecodeOptions{MaxDepth: 1})
	if err != nil || !reflect.DeepEqual(got.Document, map[string]any{"n": map[string]any{"i": int64(2)}}) {
		t.Fatalf("result=%#v err=%v", got, err)
	}
	_, err = DecodeObservedBSON(data, BSONDecodeOptions{MaxDepth: 0})
	if err != nil {
		t.Fatal(err)
	}
	_, err = DecodeObservedBSON(data, BSONDecodeOptions{MaxDepth: 1, MaxBytes: 3})
	if !errors.Is(err, ErrBSONDecodeBounds) {
		t.Fatalf("bounds err=%v", err)
	}
}

func TestDecodeObservedBSONRecognizedCursorSkipsAndStringCursorWidth(t *testing.T) {
	// 0x09 is recognized by the cursor but has no value-producing helper;
	// it is skipped and parsing continues. The string helper scans to NUL,
	// while the cursor advances by its declared length (4) plus four bytes.
	data := []byte{
		1, 0, 0, 0,
		0x09, 'd', 0, 1, 2, 3, 4, 5, 6, 7, 8,
		0x02, 's', 0, 4, 0, 0, 0, 'x', 0, 9, 9,
		0x10, 'r', 0, 3, 0, 0, 0,
		0,
	}
	got, err := DecodeObservedBSON(data, BSONDecodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Partial || got.Document["s"] != "x" || got.Document["r"] != int32(3) {
		t.Fatalf("result=%#v", got)
	}
}

func TestDecodeObservedBSONAllRecognizedCursorOnlyWidths(t *testing.T) {
	stringPayload := func(value string) []byte {
		out := make([]byte, 4, 4+len(value)+1)
		binary.LittleEndian.PutUint32(out, uint32(len(value)+1))
		out = append(out, value...)
		return append(out, 0)
	}
	payloads := [][]byte{
		{2, 0, 0, 0, 1, 0xaa, 0xbb},            // 0x05 binary
		{},                                     // 0x06 undefined
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, // 0x07 object id
		{0, 1, 2, 3, 4, 5, 6, 7},               // 0x09 date
		{'p', 0, 'o', 0},                       // 0x0b regex
		{3, 0, 0, 0, 'a', 'b', 0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, // 0x0c
		stringPayload("x"), // 0x0d javascript
		stringPayload("y"), // 0x0e symbol
		{16, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, // 0x0f code-with-scope
		{0, 1, 2, 3, 4, 5, 6, 7},                             // 0x11 timestamp
	}
	types := []byte{0x05, 0x06, 0x07, 0x09, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x11}
	for i, typ := range types {
		t.Run(fmt.Sprintf("type_%02x", typ), func(t *testing.T) {
			data := testBSONDocument(testBSONElement(typ, "s", payloads[i]), testBSONElement(0x10, "r", []byte{9, 0, 0, 0}))
			got, err := DecodeObservedBSON(data, BSONDecodeOptions{})
			if err != nil || got.Partial || !reflect.DeepEqual(got.Document, map[string]any{"r": int32(9)}) {
				t.Fatalf("result=%#v err=%v", got, err)
			}
		})
	}
}

func TestDecodeObservedBSONScalarsAndDepthFailure(t *testing.T) {
	double := make([]byte, 8)
	binary.LittleEndian.PutUint64(double, math.Float64bits(-2.5))
	min := make([]byte, 8)
	binary.LittleEndian.PutUint64(min, uint64(1)<<63)
	max := make([]byte, 8)
	binary.LittleEndian.PutUint64(max, uint64(int64(1<<63-1)))
	data := testBSONDocument(
		testBSONElement(0x01, "d", double),
		testBSONElement(0x12, "q", min),
		testBSONElement(0x06, "q", nil),
		testBSONElement(0x12, "z", max),
	)
	got, err := DecodeObservedBSON(data, BSONDecodeOptions{})
	if err != nil || got.Document["d"] != -2.5 || got.Document["q"] != int64(-1<<63) || got.Document["z"] != int64(1<<63-1) {
		t.Fatalf("result=%#v err=%v", got, err)
	}
	inner := testBSONDocument(testBSONElement(0x10, "x", []byte{1, 0, 0, 0}))
	middle := testBSONDocument(testBSONElement(0x03, "i", inner))
	outer := testBSONDocument(testBSONElement(0x03, "m", middle))
	if _, err := DecodeObservedBSON(outer, BSONDecodeOptions{MaxDepth: 1}); !errors.Is(err, ErrBSONDecodeBounds) {
		t.Fatalf("depth error=%v", err)
	}
}

func TestDecodeObservedBSONNestedUnknownDoesNotStopParent(t *testing.T) {
	// The nested cursor returns its partial dictionary at the declared
	// container boundary; the parent cursor then continues with the next key.
	data := []byte{
		1, 0, 0, 0,
		0x03, 'n', 0, 15, 0, 0, 0,
		0x10, 'i', 0, 2, 0, 0, 0,
		0x7f, 'x', 0,
		0,
		0x10, 'r', 0, 4, 0, 0, 0,
		0,
	}
	got, err := DecodeObservedBSON(data, BSONDecodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	nested, ok := got.Document["n"].(map[string]any)
	if !ok || nested["i"] != int32(2) || got.Document["r"] != int32(4) || got.Partial {
		t.Fatalf("result=%#v", got)
	}
}

func TestDecodeObservedBSONArrayKeepsEncounterOrder(t *testing.T) {
	data := []byte{
		1, 0, 0, 0,
		0x04, 'a', 0, 19, 0, 0, 0,
		0x10, '1', 0, 11, 0, 0, 0,
		0x10, '0', 0, 22, 0, 0, 0,
		0,
		0,
	}
	got, err := DecodeObservedBSON(data, BSONDecodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Document["a"], []any{int32(11), int32(22)}) {
		t.Fatalf("result=%#v", got)
	}
}

func TestDecodeObservedBSONArraySkipsNullAndStopsUnknownLocally(t *testing.T) {
	array := testBSONDocument(
		testBSONElement(0x10, "1", []byte{1, 0, 0, 0}),
		testBSONElement(0x0a, "2", nil),
		testBSONElement(0x7f, "3", nil),
	)
	root := testBSONDocument(testBSONElement(0x04, "a", array), testBSONElement(0x10, "r", []byte{7, 0, 0, 0}))
	got, err := DecodeObservedBSON(root, BSONDecodeOptions{})
	if err != nil || !reflect.DeepEqual(got.Document["a"], []any{int32(1)}) || got.Document["r"] != int32(7) {
		t.Fatalf("result=%#v err=%v", got, err)
	}
}

func TestDecodeObservedBSONNestedDeclaredWidthRegressions(t *testing.T) {
	// Nested local terminator plus padding: parent resumes at declared width.
	padding := []byte{13, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	root := testBSONDocument(testBSONElement(0x03, "n", padding), testBSONElement(0x10, "r", []byte{7, 0, 0, 0}))
	got, err := DecodeObservedBSON(root, BSONDecodeOptions{})
	if err != nil || got.Document["r"] != int32(7) {
		t.Fatalf("padding result=%#v err=%v", got, err)
	}
	// Nested value scan may pass its declared width; outer cursor still uses
	// that width and lands on the outer terminator.
	data := []byte{0, 0, 0, 0, 0x03, 'n', 0, 5, 0, 0, 0, 0x08, 0, 1, 0, 0}
	binary.LittleEndian.PutUint32(data, uint32(len(data)))
	got, err = DecodeObservedBSON(data, BSONDecodeOptions{})
	if err != nil || !reflect.DeepEqual(got.Document, map[string]any{"n": map[string]any{"": true}}) {
		t.Fatalf("beyond-width result=%#v err=%v", got, err)
	}
}

func TestDecodeObservedBSONWorkBudgetIsGlobal(t *testing.T) {
	leaf := testBSONDocument(testBSONElement(0x10, "x", []byte{1, 0, 0, 0}))
	for i := 0; i < 4; i++ {
		leaf = testBSONDocument(testBSONElement(0x03, "n", leaf))
	}
	if _, err := DecodeObservedBSON(leaf, BSONDecodeOptions{MaxDepth: 10, MaxWork: 5}); !errors.Is(err, ErrBSONDecodeBounds) {
		t.Fatalf("global work error=%v", err)
	}
}

func TestDecodeObservedBSONRecognizedDBPointerUsesDeclaredWidth(t *testing.T) {
	data := []byte{
		1, 0, 0, 0,
		0x0c, 'p', 0, 4, 0, 0, 0, 'a', 'b', 'c', 0,
		0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11,
		0x10, 'r', 0, 5, 0, 0, 0,
		0,
	}
	got, err := DecodeObservedBSON(data, BSONDecodeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Partial || got.Document["r"] != int32(5) || len(got.Document) != 1 {
		t.Fatalf("result=%#v", got)
	}
}

func TestDecodeObservedBSONGlobalWorkBound(t *testing.T) {
	// Nested declared widths can cause the source cursor to revisit suffix
	// bytes after an early nested terminator. The Go adapter bounds total work
	// independently of MaxDepth and MaxBytes.
	data := []byte{1, 0, 0, 0, 0x03, 'n', 0, 5, 0, 0, 0, 0, 0}
	_, err := DecodeObservedBSON(data, BSONDecodeOptions{MaxWork: 1})
	if !errors.Is(err, ErrBSONDecodeBounds) {
		t.Fatalf("err=%v", err)
	}
}

func TestDecodeObservedBSONInvalidUTF8ValueSkipsAndKeyErrorsOnlyForValue(t *testing.T) {
	value := []byte{1, 0, 0, 0, 0x10, 'r', 0, 1, 0, 0, 0,
		0x02, 's', 0, 3, 0, 0, 0, 0xff, 0, 0,
		0x10, 'n', 0, 2, 0, 0, 0, 0}
	got, err := DecodeObservedBSON(value, BSONDecodeOptions{})
	if err != nil || got.Document["r"] != int32(1) || got.Document["s"] != nil || got.Document["n"] != int32(2) {
		t.Fatalf("result=%#v err=%v", got, err)
	}
	invalidKeyNull := []byte{1, 0, 0, 0, 0x0a, 0xff, 0, 0}
	if _, err := DecodeObservedBSON(invalidKeyNull, BSONDecodeOptions{}); err != nil {
		t.Fatalf("invalid key with nil value should skip: %v", err)
	}
	invalidKeyValue := []byte{1, 0, 0, 0, 0x10, 0xff, 0, 1, 0, 0, 0, 0}
	if !errors.Is(mustDecodeErr(invalidKeyValue), ErrBSONDecodeInvalidKey) {
		t.Fatal("invalid key with value must fail")
	}
}

func mustDecodeErr(src []byte) error {
	_, err := DecodeObservedBSON(src, BSONDecodeOptions{})
	return err
}
