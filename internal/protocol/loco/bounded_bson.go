package loco

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// BSONDecodeOptions bounds the clean-room safety envelope around the
// source-observed decoder. The official cursor has no input-length argument;
// these limits are Go safety choices and are not protocol fields.
type BSONDecodeOptions struct {
	MaxBytes int
	MaxDepth int
}

// BSONDecodeResult preserves the official decoder's partial-dictionary result
// when an element type is not recognized. UnknownType is zero for a complete
// document or a bounded truncation error.
type BSONDecodeResult struct {
	Document    map[string]any
	Partial     bool
	UnknownType byte
}

var (
	ErrBSONDecodeBounds    = errors.New("loco: BSON decode bounds exceeded")
	ErrBSONDecodeMalformed = errors.New("loco: malformed BSON")
)

// DecodeObservedBSON decodes the source-supported BSON element subset for a
// future typed notice validator. It is deliberately not wired into Packet or
// Session decoding. The four-byte document length prefix is ignored, matching
// the observed cursor; bounds still apply to the supplied Go slice.
func DecodeObservedBSON(src []byte, options BSONDecodeOptions) (BSONDecodeResult, error) {
	if options.MaxBytes <= 0 {
		options.MaxBytes = len(src)
	}
	if options.MaxDepth <= 0 {
		options.MaxDepth = 32
	}
	if len(src) > options.MaxBytes {
		return BSONDecodeResult{}, ErrBSONDecodeBounds
	}
	if len(src) < 4 {
		return BSONDecodeResult{}, ErrBSONDecodeMalformed
	}
	value, _, unknown, err := decodeObservedDocument(src, 4, options, 0, false)
	if err != nil {
		return BSONDecodeResult{}, err
	}
	return BSONDecodeResult{Document: value.(map[string]any), Partial: unknown != 0, UnknownType: unknown}, nil
}

func decodeObservedDocument(src []byte, pos int, options BSONDecodeOptions, depth int, array bool) (any, int, byte, error) {
	if depth > options.MaxDepth {
		return nil, pos, 0, ErrBSONDecodeBounds
	}
	if array {
		out := []any{}
		for {
			if pos >= len(src) {
				return nil, pos, 0, ErrBSONDecodeMalformed
			}
			typ := src[pos]
			if typ == 0 {
				return out, pos + 1, 0, nil
			}
			_, valueStart, err := readCString(src, pos+1)
			if err != nil {
				return nil, pos, 0, err
			}
			value, next, unknown, err := decodeObservedElement(src, valueStart, typ, options, depth)
			if err != nil {
				return nil, pos, 0, err
			}
			if unknown != 0 {
				return out, next, unknown, nil
			}
			if value != skipBSONValue {
				out = append(out, value)
			}
			pos = next
		}
	}
	out := make(map[string]any)
	for {
		if pos >= len(src) {
			return nil, pos, 0, ErrBSONDecodeMalformed
		}
		typ := src[pos]
		if typ == 0 {
			return out, pos + 1, 0, nil
		}
		key, valueStart, err := readCString(src, pos+1)
		if err != nil {
			return nil, pos, 0, err
		}
		value, next, unknown, err := decodeObservedElement(src, valueStart, typ, options, depth)
		if err != nil {
			return nil, pos, 0, err
		}
		if unknown != 0 {
			return out, next, unknown, nil
		}
		// Source null/undefined elements consume their type/key but do not
		// assign, preserving an earlier duplicate value.
		if value != skipBSONValue {
			out[key] = value
		}
		pos = next
	}
}

type skipValue struct{}

var skipBSONValue = skipValue{}

func decodeObservedElement(src []byte, pos int, typ byte, options BSONDecodeOptions, depth int) (any, int, byte, error) {
	switch typ {
	case 0x06, 0x0a: // undefined/null: source skips assignment and continues.
		return skipBSONValue, pos, 0, nil
	case 0x01:
		if len(src)-pos < 8 {
			return nil, pos, 0, ErrBSONDecodeMalformed
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(src[pos : pos+8])), pos + 8, 0, nil
	case 0x08:
		if len(src)-pos < 1 {
			return nil, pos, 0, ErrBSONDecodeMalformed
		}
		return src[pos] != 0, pos + 1, 0, nil
	case 0x10:
		if len(src)-pos < 4 {
			return nil, pos, 0, ErrBSONDecodeMalformed
		}
		return int32(binary.LittleEndian.Uint32(src[pos : pos+4])), pos + 4, 0, nil
	case 0x12:
		if len(src)-pos < 8 {
			return nil, pos, 0, ErrBSONDecodeMalformed
		}
		return int64(binary.LittleEndian.Uint64(src[pos : pos+8])), pos + 8, 0, nil
	case 0x02:
		if len(src)-pos < 4 {
			return nil, pos, 0, ErrBSONDecodeMalformed
		}
		length := int64(int32(binary.LittleEndian.Uint32(src[pos : pos+4])))
		if length <= 0 || length > int64(len(src)-pos-4) {
			return nil, pos, 0, ErrBSONDecodeMalformed
		}
		body := src[pos+4 : pos+4+int(length)]
		if body[len(body)-1] != 0 {
			return nil, pos, 0, ErrBSONDecodeMalformed
		}
		return string(body[:len(body)-1]), pos + 4 + int(length), 0, nil
	case 0x03:
		if len(src)-pos < 4 {
			return nil, pos, 0, ErrBSONDecodeMalformed
		}
		return decodeObservedDocument(src, pos+4, options, depth+1, false)
	case 0x04:
		if len(src)-pos < 4 {
			return nil, pos, 0, ErrBSONDecodeMalformed
		}
		return decodeObservedDocument(src, pos+4, options, depth+1, true)
	default:
		// The official helper returns no value for an unknown type; the outer
		// loop returns the dictionary accumulated before that element.
		return nil, pos, typ, nil
	}
}

func readCString(src []byte, pos int) (string, int, error) {
	for i := pos; i < len(src); i++ {
		if src[i] == 0 {
			return string(src[pos:i]), i + 1, nil
		}
	}
	return "", pos, fmt.Errorf("%w: unterminated key", ErrBSONDecodeMalformed)
}
