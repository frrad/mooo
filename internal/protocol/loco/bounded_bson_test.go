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
	data := []byte{5, 0, 0, 0, 0x03, 'n', 0, 12, 0, 0, 0, 0x12, 'i', 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0}
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
