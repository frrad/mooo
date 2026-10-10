package strictjson

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// objectWithKeys returns {"k0":0,...} with n distinct keys.
func objectWithKeys(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = `"k` + strconv.Itoa(i) + `":0`
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func nestedArrays(depth int) string {
	return strings.Repeat("[", depth) + strings.Repeat("]", depth)
}

func arrayOf(n int) string {
	return "[" + strings.TrimSuffix(strings.Repeat("0,", n), ",") + "]"
}

// The attachment decoders in media, events/location, events/profile and
// chatmeta check only the root object.
var shallow = Limits{MaxBytes: 64 << 10, MaxKeys: 64}

// The events post/vote/feed decoders check every nested container.
var nested = Limits{MaxBytes: 64 << 10, MaxKeys: 64, Nested: true, MaxDepth: 16, MaxArray: 128, MaxValues: 1024}

func TestUnmarshalAdmission(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		limits Limits
		want   error
	}{
		{name: "empty object", input: `{}`, limits: shallow},
		{name: "surrounding whitespace", input: " \t\r\n{\"a\":1} \n", limits: shallow},
		{name: "empty input", input: ``, limits: shallow, want: ErrInvalid},
		{name: "whitespace only", input: "  ", limits: shallow, want: ErrInvalid},
		{name: "array root", input: `[]`, limits: shallow, want: ErrInvalid},
		{name: "string root", input: `"x"`, limits: shallow, want: ErrInvalid},
		{name: "null root", input: `null`, limits: shallow, want: ErrInvalid},
		{name: "duplicate root key", input: `{"a":1,"a":1}`, limits: shallow, want: ErrInvalid},
		{name: "escaped duplicate root key", input: `{"a":1,"a":2}`, limits: shallow, want: ErrInvalid},
		{name: "keys differing only in case", input: `{"a":1,"A":2}`, limits: shallow},
		{name: "64 keys", input: objectWithKeys(64), limits: shallow},
		{name: "65 keys", input: objectWithKeys(65), limits: shallow, want: ErrInvalid},
		{name: "65 keys without a key limit", input: objectWithKeys(65), limits: Limits{}},
		{name: "duplicate key without a key limit", input: `{"a":1,"a":1}`, limits: Limits{}, want: ErrInvalid},
		{name: "trailing object", input: `{}{}`, limits: shallow, want: ErrInvalid},
		{name: "trailing scalar", input: `{} 1`, limits: shallow, want: ErrInvalid},
		{name: "trailing garbage", input: `{}x`, limits: shallow, want: ErrInvalid},
		{name: "unterminated object", input: `{"a":1`, limits: shallow, want: ErrInvalid},
		{name: "missing value", input: `{"a":}`, limits: shallow, want: ErrInvalid},
		{name: "non-string key", input: `{1:2}`, limits: shallow, want: ErrInvalid},
		{name: "invalid nested value", input: `{"a":[1,}`, limits: shallow, want: ErrInvalid},
		{name: "at size limit", input: `{"a":"` + strings.Repeat("x", 100-8) + `"}`, limits: Limits{MaxBytes: 100}},
		{name: "over size limit", input: `{"a":"` + strings.Repeat("x", 100-7) + `"}`, limits: Limits{MaxBytes: 100}, want: ErrTooLarge},
		{name: "over size limit and not an object", input: strings.Repeat(" ", 101), limits: Limits{MaxBytes: 100}, want: ErrTooLarge},
		{name: "shallow ignores nested duplicate", input: `{"a":{"b":1,"b":2}}`, limits: shallow},
		{name: "shallow ignores nested key count", input: `{"a":` + objectWithKeys(65) + `}`, limits: shallow},
		{name: "shallow ignores nested depth", input: `{"a":` + nestedArrays(40) + `}`, limits: shallow},
		{name: "nested duplicate", input: `{"a":{"b":1,"b":2}}`, limits: nested, want: ErrInvalid},
		{name: "duplicate inside array", input: `{"a":[{"b":1,"b":2}]}`, limits: nested, want: ErrInvalid},
		{name: "nested 65 keys", input: `{"a":` + objectWithKeys(65) + `}`, limits: nested, want: ErrInvalid},
		{name: "depth 16", input: `{"a":` + nestedArrays(16) + `}`, limits: nested},
		{name: "depth 17", input: `{"a":` + nestedArrays(17) + `}`, limits: nested, want: ErrInvalid},
		{name: "128 array elements", input: `{"a":` + arrayOf(128) + `}`, limits: nested},
		{name: "129 array elements", input: `{"a":` + arrayOf(129) + `}`, limits: nested, want: ErrInvalid},
		{name: "1024 values", input: `{"a":` + arrayOf(127) + `,"b":[` + strings.Repeat(arrayOf(126)+",", 7) + arrayOf(4) + `]}`, limits: nested},
		{name: "1025 values", input: `{"a":` + arrayOf(127) + `,"b":[` + strings.Repeat(arrayOf(126)+",", 7) + arrayOf(5) + `]}`, limits: nested, want: ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v map[string]any
			err := Unmarshal([]byte(tt.input), &v, tt.limits)
			if !errors.Is(err, tt.want) || tt.want == nil && err != nil {
				t.Fatalf("Unmarshal = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestUnmarshalDecodesIntoTarget(t *testing.T) {
	var got struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	if err := Unmarshal([]byte(`{"name":"synthetic","count":3,"extra":[1,2]}`), &got, shallow); err != nil {
		t.Fatal(err)
	}
	if got.Name != "synthetic" || got.Count != 3 {
		t.Fatalf("got %+v", got)
	}
}

func TestUnmarshalTypeMismatchIsInvalid(t *testing.T) {
	var got struct {
		Count int `json:"count"`
	}
	if err := Unmarshal([]byte(`{"count":"3"}`), &got, shallow); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Unmarshal = %v, want %v", err, ErrInvalid)
	}
}

func TestValidateDoesNotConvertNumbers(t *testing.T) {
	// Admission does not convert numbers, so an out-of-range float is left
	// to the caller's decode, which rejects it for a float64 target.
	if err := Validate([]byte(`{"a":1e400}`), nested); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
	var got map[string]float64
	if err := Unmarshal([]byte(`{"a":1e400}`), &got, nested); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Unmarshal = %v, want %v", err, ErrInvalid)
	}
}

func TestValidateAcceptsAnyRoot(t *testing.T) {
	for _, input := range []string{`[]`, `[{"a":1}]`, `"x"`, `1`, `null`, `true`} {
		if err := Validate([]byte(input), nested); err != nil {
			t.Errorf("Validate(%s) = %v, want nil", input, err)
		}
	}
	for _, input := range []string{`[{"a":1,"a":2}]`, arrayOf(129), `[] []`, ``} {
		if err := Validate([]byte(input), nested); !errors.Is(err, ErrInvalid) {
			t.Errorf("Validate(%s) = %v, want %v", input, err, ErrInvalid)
		}
	}
}
