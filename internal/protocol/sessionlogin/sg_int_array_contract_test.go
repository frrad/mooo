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
