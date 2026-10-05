package sessionlogin

import (
	"math"
	"testing"
)

func TestCoerceFoundationTiCapturedScalarDomain(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want int32
	}{
		{"int32", int32(-7), -7},
		{"int64_low_word", int64(4294967297), 1},
		{"bool_false", false, 0},
		{"bool_true", true, 1},
		{"double_negative_fraction", -3.75, -3},
		{"double_positive_fraction", 3.75, 3},
		{"double_int32_max", 2147483647.0, 2147483647},
		{"double_above_int32", 2147483648.0, math.MinInt32},
		{"double_positive_huge", 1e40, -1},
		{"double_negative_huge", -1e40, 0},
		{"double_positive_infinity", math.Inf(1), -1},
		{"double_negative_infinity", math.Inf(-1), 0},
		{"double_nan", math.NaN(), 0},
		{"double_negative_zero", math.Copysign(0, -1), 0},
		{"double_two32_minus_fraction", 4294967295.5, -1},
		{"double_two32_plus_fraction", 4294967296.5, 0},
		{"double_negative_two32_minus_fraction", -4294967296.5, 0},
		{"double_negative_two32_plus_fraction", -4294967295.5, 1},
		{"double_two63", 9223372036854775808.0, -1},
		{"double_negative_two63", -9223372036854775808.0, 0},
		{"double_two63_nearest_below", 9223372036854774784.0, -1024},
		{"double_negative_two63_nearest_below", -9223372036854774784.0, 1024},
		{"double_two32_plus_fraction_far", 4294967297.75, 1},
		{"double_negative_two32_plus_fraction_far", -4294967297.75, -1},
		{"double_positive_subnormal", math.SmallestNonzeroFloat64, 0},
		{"double_negative_subnormal", -math.SmallestNonzeroFloat64, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := coerceFoundationTi(tc.in)
			if !ok || got != tc.want {
				t.Fatalf("got %d/%t want %d", got, ok, tc.want)
			}
		})
	}
}

func TestCoerceFoundationTiRejectsUntracedObjects(t *testing.T) {
	for _, value := range []any{nil, "42", []byte{42}, map[string]any{"r": 42}} {
		if got, ok := coerceFoundationTi(value); ok || got != 0 {
			t.Fatalf("value %#v got %d/%t; want rejected", value, got, ok)
		}
	}
}
