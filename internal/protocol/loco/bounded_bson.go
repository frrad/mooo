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
	value, _, unknown, err := decodeObservedDocument(src, 4, len(src), options, 0, false, true)
	if err != nil {
		return BSONDecodeResult{}, err
	}
	return BSONDecodeResult{Document: value.(map[string]any), Partial: unknown != 0, UnknownType: unknown}, nil
}

func decodeObservedDocument(src []byte, pos, limit int, options BSONDecodeOptions, depth int, array, propagateUnknown bool) (any, int, byte, error) {
	if depth > options.MaxDepth {
		return nil, pos, 0, ErrBSONDecodeBounds
	}
	if array {
		out := []any{}
		for {
			if pos >= limit || pos >= len(src) {
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
				if propagateUnknown {
					return out, next, unknown, nil
				}
				return out, limit, 0, nil
			}
			if value != skipBSONValue {
				out = append(out, value)
			}
			pos = next
		}
	}
	out := make(map[string]any)
	for {
		if pos >= limit || pos >= len(src) {
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
			if propagateUnknown {
				return out, next, unknown, nil
			}
			return out, limit, 0, nil
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
	case 0x05, 0x06, 0x07, 0x09, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x11:
		// These recognized cursor types have no value branch in the observed
		// helper. The cursor advances over the payload and continues; they are
		// different from an unrecognized type, which returns the partial map.
		next, err := skipObservedValue(src, pos, typ)
		if err != nil {
			return nil, pos, 0, err
		}
		return skipBSONValue, next, 0, nil
	case 0x0a:
		// Null has no payload: source skips assignment and continues.
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
		body := src[pos+4:]
		nul := -1
		for i, b := range body {
			if b == 0 {
				nul = i
				break
			}
		}
		if nul < 0 {
			return nil, pos, 0, ErrBSONDecodeMalformed
		}
		return string(body[:nul]), pos + 4 + int(length), 0, nil
	case 0x03:
		if len(src)-pos < 4 {
			return nil, pos, 0, ErrBSONDecodeMalformed
		}
		length := int64(int32(binary.LittleEndian.Uint32(src[pos : pos+4])))
		if length < 5 || length > int64(len(src)-pos) {
			return nil, pos, 0, ErrBSONDecodeMalformed
		}
		value, _, unknown, err := decodeObservedDocument(src, pos+4, pos+int(length), options, depth+1, false, false)
		return value, pos + int(length), unknown, err
	case 0x04:
		if len(src)-pos < 4 {
			return nil, pos, 0, ErrBSONDecodeMalformed
		}
		length := int64(int32(binary.LittleEndian.Uint32(src[pos : pos+4])))
		if length < 5 || length > int64(len(src)-pos) {
			return nil, pos, 0, ErrBSONDecodeMalformed
		}
		value, _, unknown, err := decodeObservedDocument(src, pos+4, pos+int(length), options, depth+1, true, false)
		return value, pos + int(length), unknown, err
	default:
		// The official helper returns no value for an unknown type; the outer
		// loop returns the dictionary accumulated before that element.
		return nil, pos, typ, nil
	}
}

func skipObservedValue(src []byte, pos int, typ byte) (int, error) {
	readN := func(n int) (int, error) {
		if n < 0 || n > len(src)-pos {
			return pos, ErrBSONDecodeMalformed
		}
		return pos + n, nil
	}
	readCStringEnd := func(at int) (int, error) {
		for i := at; i < len(src); i++ {
			if src[i] == 0 {
				return i + 1, nil
			}
		}
		return at, ErrBSONDecodeMalformed
	}
	switch typ {
	case 0x05: // binary: int32 byte count, subtype, bytes
		if len(src)-pos < 5 {
			return pos, ErrBSONDecodeMalformed
		}
		n := int64(int32(binary.LittleEndian.Uint32(src[pos : pos+4])))
		if n < 0 {
			return pos, ErrBSONDecodeMalformed
		}
		return readN(5 + int(n))
	case 0x07:
		return readN(12)
	case 0x09, 0x11:
		return readN(8)
	case 0x0b:
		end, err := readCStringEnd(pos)
		if err != nil {
			return pos, err
		}
		return readCStringEnd(end)
	case 0x0c:
		end, err := readCStringEnd(pos)
		if err != nil {
			return pos, err
		}
		return readN((end - pos) + 12)
	case 0x0d, 0x0e:
		if len(src)-pos < 4 {
			return pos, ErrBSONDecodeMalformed
		}
		n := int64(int32(binary.LittleEndian.Uint32(src[pos : pos+4])))
		if n < 0 {
			return pos, ErrBSONDecodeMalformed
		}
		return readN(4 + int(n))
	case 0x0f:
		if len(src)-pos < 4 {
			return pos, ErrBSONDecodeMalformed
		}
		n := int64(int32(binary.LittleEndian.Uint32(src[pos : pos+4])))
		if n < 0 {
			return pos, ErrBSONDecodeMalformed
		}
		return readN(int(n))
	case 0x06, 0x0a:
		return pos, nil
	default:
		return pos, ErrBSONDecodeMalformed
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
