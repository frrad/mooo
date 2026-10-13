// Package bsonfield reads typed fields from LOCO BSON documents.
//
// Every accessor takes a document and one or more keys and returns
// (value, present, err). With several keys the first present key wins, so a
// caller can list a field's current name before its legacy aliases. A missing
// field is (zero, false, nil); a present field of the wrong BSON type is
// (zero, false, err) where err wraps the Reader's sentinel.
//
// Integer widening is the same everywhere: Int64, Int32 and Int64s accept BSON
// int32 and int64 and widen both to int64. Int64Exact accepts only BSON
// int64. No accessor accepts BSON double, decimal128 or a numeric string.
package bsonfield

import (
	"errors"
	"fmt"
	"math"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// ErrType is the sentinel a zero Reader wraps for a field of the wrong type.
var ErrType = errors.New("bsonfield: unexpected BSON type")

// Reader holds a protocol package's field-reading policy.
type Reader struct {
	// Err is wrapped by every type error, so callers keep their own
	// errors.Is sentinel. Nil means ErrType.
	Err error
	// NullAbsent treats BSON null and undefined as absent: lookup moves on to
	// the next key and an all-null field is not present. Without it, null is
	// a present value of the wrong type.
	NullAbsent bool
}

// Lookup returns the value of the first present key.
func (r Reader) Lookup(raw bson.Raw, keys ...string) (bson.RawValue, bool) {
	for _, key := range keys {
		value, err := raw.LookupErr(key)
		if err != nil {
			continue
		}
		if r.NullAbsent && (value.Type == bson.TypeNull || value.Type == bson.TypeUndefined) {
			continue
		}
		return value, true
	}
	return bson.RawValue{}, false
}

// Int64Value widens a BSON int32 or int64 to int64.
func Int64Value(value bson.RawValue) (int64, bool) {
	switch value.Type {
	case bson.TypeInt32:
		return int64(value.Int32()), true
	case bson.TypeInt64:
		return value.Int64(), true
	default:
		return 0, false
	}
}

// Int64 reads a BSON int32 or int64 field as int64.
func (r Reader) Int64(raw bson.Raw, keys ...string) (int64, bool, error) {
	value, ok := r.Lookup(raw, keys...)
	if !ok {
		return 0, false, nil
	}
	n, ok := Int64Value(value)
	if !ok {
		return 0, false, r.typeError(keys)
	}
	return n, true, nil
}

// Int64OrZero reads an int32 or int64 field leniently: an absent field or a
// field of any other type is zero.
func Int64OrZero(raw bson.Raw, keys ...string) int64 {
	n, _, err := Reader{}.Int64(raw, keys...)
	if err != nil {
		return 0
	}
	return n
}

// Int64Exact reads a field that must be a BSON int64; int32 is a type error.
func (r Reader) Int64Exact(raw bson.Raw, keys ...string) (int64, bool, error) {
	value, ok := r.Lookup(raw, keys...)
	if !ok {
		return 0, false, nil
	}
	if value.Type != bson.TypeInt64 {
		return 0, false, r.typeError(keys)
	}
	return value.Int64(), true, nil
}

// Int32 reads a BSON int32 or int64 field and rejects values outside the
// int32 range.
func (r Reader) Int32(raw bson.Raw, keys ...string) (int32, bool, error) {
	n, ok, err := r.Int64(raw, keys...)
	if err != nil || !ok {
		return 0, ok, err
	}
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, false, fmt.Errorf("%w: %s out of int32 range", r.sentinel(), keys[0])
	}
	return int32(n), true, nil
}

// String reads a BSON string field.
func (r Reader) String(raw bson.Raw, keys ...string) (string, bool, error) {
	value, ok := r.Lookup(raw, keys...)
	if !ok {
		return "", false, nil
	}
	if value.Type != bson.TypeString {
		return "", false, r.typeError(keys)
	}
	return value.StringValue(), true, nil
}

// Bool reads a BSON boolean field.
func (r Reader) Bool(raw bson.Raw, keys ...string) (bool, bool, error) {
	value, ok := r.Lookup(raw, keys...)
	if !ok {
		return false, false, nil
	}
	if value.Type != bson.TypeBoolean {
		return false, false, r.typeError(keys)
	}
	return value.Boolean(), true, nil
}

// Int64s reads a BSON array whose elements are all int32 or int64.
func (r Reader) Int64s(raw bson.Raw, keys ...string) ([]int64, bool, error) {
	values, ok, err := r.array(raw, keys)
	if err != nil || !ok {
		return nil, false, err
	}
	result := make([]int64, 0, len(values))
	for _, element := range values {
		n, ok := Int64Value(element)
		if !ok {
			return nil, false, fmt.Errorf("%w: %s element", r.sentinel(), keys[0])
		}
		result = append(result, n)
	}
	return result, true, nil
}

// Strings reads a BSON array whose elements are all strings.
func (r Reader) Strings(raw bson.Raw, keys ...string) ([]string, bool, error) {
	values, ok, err := r.array(raw, keys)
	if err != nil || !ok {
		return nil, false, err
	}
	result := make([]string, 0, len(values))
	for _, element := range values {
		if element.Type != bson.TypeString {
			return nil, false, fmt.Errorf("%w: %s element", r.sentinel(), keys[0])
		}
		result = append(result, element.StringValue())
	}
	return result, true, nil
}

// Document reads an embedded document field. The result is a validated copy
// that does not alias raw.
func (r Reader) Document(raw bson.Raw, keys ...string) (bson.Raw, bool, error) {
	value, ok := r.Lookup(raw, keys...)
	if !ok {
		return nil, false, nil
	}
	if value.Type != bson.TypeEmbeddedDocument {
		return nil, false, r.typeError(keys)
	}
	document := append(bson.Raw(nil), value.Document()...)
	if err := document.Validate(); err != nil {
		return nil, false, r.typeError(keys)
	}
	return document, true, nil
}

func (r Reader) array(raw bson.Raw, keys []string) ([]bson.RawValue, bool, error) {
	value, ok := r.Lookup(raw, keys...)
	if !ok {
		return nil, false, nil
	}
	if value.Type != bson.TypeArray {
		return nil, false, r.typeError(keys)
	}
	values, err := value.Array().Values()
	if err != nil {
		return nil, false, r.typeError(keys)
	}
	return values, true, nil
}

func (r Reader) sentinel() error {
	if r.Err != nil {
		return r.Err
	}
	return ErrType
}

func (r Reader) typeError(keys []string) error {
	return fmt.Errorf("%w: %s", r.sentinel(), keys[0])
}

// Shape describes a document by key names and BSON types only, recursing into
// embedded documents, for example "{k:string vh:string p:int32}". It never
// includes a value, so it is safe to log for a reply that failed to decode.
// An invalid document is described as "invalid".
func Shape(raw bson.Raw) string {
	return shape(raw, 0)
}

func shape(raw bson.Raw, depth int) string {
	elements, err := raw.Elements()
	if err != nil {
		return "invalid"
	}
	out := "{"
	for i, element := range elements {
		if i > 0 {
			out += " "
		}
		value := element.Value()
		out += element.Key() + ":"
		if value.Type == bson.TypeEmbeddedDocument && depth < 4 {
			out += shape(value.Document(), depth+1)
			continue
		}
		out += shapeType(value.Type)
	}
	return out + "}"
}

func shapeType(t bson.Type) string {
	switch t {
	case bson.TypeString:
		return "string"
	case bson.TypeInt32:
		return "int32"
	case bson.TypeInt64:
		return "int64"
	case bson.TypeDouble:
		return "double"
	case bson.TypeBoolean:
		return "bool"
	case bson.TypeEmbeddedDocument:
		return "document"
	case bson.TypeArray:
		return "array"
	case bson.TypeNull:
		return "null"
	case bson.TypeBinary:
		return "binary"
	}
	return fmt.Sprintf("type_0x%02x", byte(t))
}
