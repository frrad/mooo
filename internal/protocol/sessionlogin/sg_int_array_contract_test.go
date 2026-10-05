package sessionlogin

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
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
			Name          string `json:"name"`
			Factory       string `json:"factory"`
			Class         string `json:"class"`
			ObjCType      string `json:"objc_type"`
			Int64Value    int64  `json:"int64_value"`
			DoubleBits    string `json:"double_bits"`
			BoolValue     bool   `json:"bool_value"`
			StringValue   string `json:"string_value"`
			ExpectedInt   *int64 `json:"expected_int"`
			ExpectedLong  *int64 `json:"expected_long"`
			ExpectedError string `json:"expected_error"`
		} `json:"element_cases"`
	}
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.ElementCases) != 19 {
		t.Fatalf("element cases=%d, want 19", len(fixture.ElementCases))
	}
	for _, tc := range fixture.ElementCases {
		t.Run(tc.Name, func(t *testing.T) {
			if tc.Class == "" || tc.ObjCType == "" || tc.Factory == "" {
				t.Fatalf("missing factory provenance: %#v", tc)
			}
			gotInt, gotLong, gotErr := modelSGArrayFactoryInput(tc.Factory, tc.Int64Value, tc.DoubleBits, tc.BoolValue, tc.StringValue)
			if tc.ExpectedError != "" {
				if gotErr == nil || gotErr.Error() != tc.ExpectedError {
					t.Fatalf("error=%v want %s", gotErr, tc.ExpectedError)
				}
				return
			}
			if gotErr != nil || tc.ExpectedInt == nil || tc.ExpectedLong == nil {
				t.Fatalf("conversion int=%d long=%d err=%v", gotInt, gotLong, gotErr)
			}
			if gotInt != int32(*tc.ExpectedInt) || gotLong != *tc.ExpectedLong {
				t.Fatalf("int=%d/long=%d want int=%d/long=%d", gotInt, gotLong, *tc.ExpectedInt, *tc.ExpectedLong)
			}
		})
	}
}

var errFoundationInvalidArgument = errors.New("NSInvalidArgumentException")

// modelSGArrayFactoryInput models only the captured Foundation factory inputs.
// It does not infer a general string grammar or use the fixture name as input.
func modelSGArrayFactoryInput(factory string, int64Value int64, doubleBits string, boolValue bool, stringValue string) (int32, int64, error) {
	switch factory {
	case "numberWithLongLong":
		return int32(uint32(int64Value)), int64Value, nil
	case "numberWithBool":
		if boolValue {
			return 1, 1, nil
		}
		return 0, 0, nil
	case "numberWithDouble":
		bits, err := hexDecode(doubleBits)
		if err != nil || len(bits) != 8 {
			return 0, 0, errFoundationInvalidArgument
		}
		value := math.Float64frombits(binary.LittleEndian.Uint64(bits))
		longValue := foundationDoubleLong(value)
		return int32(uint32(longValue)), longValue, nil
	case "initWithUTF8String":
		longValue := foundationCapturedStringLong(stringValue)
		return int32(uint32(longValue)), longValue, nil
	case "NSNull", "NSArray", "NSDictionary":
		return 0, 0, errFoundationInvalidArgument
	default:
		return 0, 0, errFoundationInvalidArgument
	}
}

func foundationDoubleLong(value float64) int64 {
	if math.IsNaN(value) {
		return 0
	}
	const maxInt64AsFloat = 9223372036854775808.0
	if math.IsInf(value, 1) || value >= maxInt64AsFloat {
		return math.MaxInt64
	}
	if math.IsInf(value, -1) || value <= -maxInt64AsFloat {
		return math.MinInt64
	}
	return int64(value)
}

func foundationCapturedStringLong(value string) int64 {
	pos, sign := 0, int64(1)
	if len(value) > 0 && (value[0] == '+' || value[0] == '-') {
		if value[0] == '-' {
			sign = -1
		}
		pos++
	}
	start := pos
	var integer int64
	for pos < len(value) && value[pos] >= '0' && value[pos] <= '9' {
		integer = integer*10 + int64(value[pos]-'0')
		pos++
	}
	if pos == start {
		return 0
	}
	return sign * integer
}
