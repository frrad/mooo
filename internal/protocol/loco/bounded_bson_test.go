package loco

import (
	"errors"
	"reflect"
	"testing"
)

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
