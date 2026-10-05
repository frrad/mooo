package sessionlogin

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type sgIntArrayFixture struct {
	Status string           `json:"status"`
	Cases  []sgIntArrayCase `json:"cases"`
	Gaps   []string         `json:"gaps"`
}

type sgIntArrayCase struct {
	Name          string  `json:"name"`
	Class         string  `json:"class"`
	Superclass    string  `json:"superclass"`
	Width         int     `json:"width"`
	Values        []int64 `json:"values"`
	NumberValues  []int64 `json:"number_values"`
	ExpectedData  string  `json:"expected_data_hex"`
	ExpectedCount int     `json:"expected_count"`
}

func modelSGIntArray(values []int64, width int) []byte {
	out := make([]byte, len(values)*width)
	for i, value := range values {
		switch width {
		case 4:
			binary.LittleEndian.PutUint32(out[i*4:], uint32(int32(value)))
		case 8:
			binary.LittleEndian.PutUint64(out[i*8:], uint64(value))
		}
	}
	return out
}

func TestSGIntArrayFrameworkContractFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-sg-int-array.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture sgIntArrayFixture
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Status != "reviewed-static-framework-bounded-synthetic" || len(fixture.Cases) != 7 || len(fixture.Gaps) != 3 {
		t.Fatalf("fixture header=%#v", fixture)
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			if (tc.Width != 4 && tc.Width != 8) || tc.ExpectedCount < 0 {
				t.Fatalf("invalid width/count: %#v", tc)
			}
			if tc.Width == 4 && (tc.Class != "SGInt32Array" || tc.Superclass != "SGIntArray") {
				t.Fatalf("int32 inheritance=%s -> %s", tc.Class, tc.Superclass)
			}
			if tc.Width == 8 && (tc.Class != "SGInt64Array" || tc.Superclass != "SGLongArray") {
				t.Fatalf("int64 inheritance=%s -> %s", tc.Class, tc.Superclass)
			}
			values := tc.Values
			if tc.NumberValues != nil {
				values = tc.NumberValues
			}
			got := modelSGIntArray(values, tc.Width)
			want, err := hexDecode(tc.ExpectedData)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) || len(values) != tc.ExpectedCount {
				t.Fatalf("data=%x/count=%d want data=%x/count=%d", got, len(values), want, tc.ExpectedCount)
			}
		})
	}
}

func hexDecode(s string) ([]byte, error) {
	out := make([]byte, len(s)/2)
	for i := range out {
		var n byte
		for _, c := range []byte{s[2*i], s[2*i+1]} {
			n <<= 4
			switch {
			case c >= '0' && c <= '9':
				n += c - '0'
			case c >= 'a' && c <= 'f':
				n += c - 'a' + 10
			case c >= 'A' && c <= 'F':
				n += c - 'A' + 10
			default:
				return nil, os.ErrInvalid
			}
		}
		out[i] = n
	}
	return out, nil
}

func TestSGIntArrayElementCoercionFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-sg-int-array.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		ElementCases []struct {
			Name         string `json:"name"`
			Class        string `json:"class"`
			Width        int    `json:"width"`
			Kind         string `json:"kind"`
			ExpectedData string `json:"expected_data_hex"`
			FailureIndex int    `json:"failure_index"`
		} `json:"element_cases"`
	}
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.ElementCases) != 12 {
		t.Fatalf("element cases=%d, want 12", len(fixture.ElementCases))
	}
	for _, tc := range fixture.ElementCases {
		t.Run(tc.Name, func(t *testing.T) {
			got, failureIndex, ok := modelSGArrayElement(tc.Kind, tc.Width)
			want, err := hexDecode(tc.ExpectedData)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				if failureIndex != tc.FailureIndex {
					t.Fatalf("failure index=%d want %d", failureIndex, tc.FailureIndex)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("prefix=%x want %x", got, want)
				}
				return
			}
			if tc.FailureIndex != 0 || !bytes.Equal(got, want) {
				t.Fatalf("data=%x/failure=%d want data=%x/no failure", got, tc.FailureIndex, want)
			}
		})
	}
}

// modelSGArrayElement contains only the captured Foundation values. The
// strings are probe inputs, not a general numeric-string parser.
func modelSGArrayElement(kind string, width int) ([]byte, int, bool) {
	var value int64
	switch kind {
	case "bool_true":
		value = 1
	case "bool_false", "string_invalid":
		value = 0
	case "int64_above_int32":
		value = 4294967297
	case "double_fraction":
		value = 3
	case "double_negative_fraction", "string_fraction":
		value = -3
	case "positive_infinity":
		value = 2147483647
	case "negative_infinity":
		value = -9223372036854775808
	case "string_decimal":
		value = 42
	case "nsnull_after_prefix", "array_after_prefix":
		prefix := modelSGIntArray([]int64{1}, width)
		return prefix, 1, false
	default:
		return nil, 0, false
	}
	return modelSGIntArray([]int64{value}, width), 0, true
}
