package sessionlogin

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

type bsonScalarFixture struct {
	Status string           `json:"status"`
	Cases  []bsonScalarCase `json:"cases"`
}

type bsonScalarCase struct {
	Name       string `json:"name"`
	ObjCType   string `json:"objc_type"`
	Value      string `json:"value"`
	BSONType   byte   `json:"bson_type"`
	PayloadHex string `json:"payload_hex"`
	Width      int    `json:"width"`
}

func projectBSONScalar(c bsonScalarCase) (byte, []byte, error) {
	var out []byte
	var bsonType byte
	switch c.ObjCType {
	case "B", "c":
		bsonType = 8
	case "d":
		bsonType = 1
	case "i":
		bsonType = 16
	case "q":
		bsonType = 18
	case "null":
		bsonType = 10
	default:
		return 0, nil, fmt.Errorf("unsupported objc type %q", c.ObjCType)
	}
	switch c.ObjCType {
	case "B", "c":
		v, err := strconv.ParseBool(c.Value)
		if err != nil {
			return 0, nil, err
		}
		out = []byte{0}
		if v {
			out[0] = 1
		}
	case "d":
		v, err := strconv.ParseFloat(c.Value, 64)
		if err != nil {
			return 0, nil, err
		}
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], math.Float64bits(v))
		out = b[:]
	case "i":
		var b [4]byte
		var v int64
		if _, err := fmt.Sscan(c.Value, &v); err != nil {
			return 0, nil, err
		}
		binary.LittleEndian.PutUint32(b[:], uint32(int32(v)))
		out = b[:]
	case "q":
		var b [8]byte
		var v int64
		if _, err := fmt.Sscan(c.Value, &v); err != nil {
			return 0, nil, err
		}
		binary.LittleEndian.PutUint64(b[:], uint64(v))
		out = b[:]
	case "null":
		out = nil
	default:
		return 0, nil, fmt.Errorf("unsupported objc type %q", c.ObjCType)
	}
	return bsonType, out, nil
}

func TestBSONScalarFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-bson-scalars.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f bsonScalarFixture
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		t.Fatal(err)
	}
	if f.Status != "reviewed-static-synthetic" || len(f.Cases) != 13 {
		t.Fatalf("header %#v", f)
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		if c.Name == "" || seen[c.Name] {
			t.Fatalf("duplicate/empty case %q", c.Name)
		}
		seen[c.Name] = true
		gotType, got, err := projectBSONScalar(c)
		if err != nil {
			t.Fatal(err)
		}
		want, err := hex.DecodeString(c.PayloadHex)
		if err != nil {
			t.Fatal(err)
		}
		if gotType != c.BSONType || string(got) != string(want) || len(got) != c.Width {
			t.Errorf("%s payload=%x width=%d want=%s/%d", c.Name, got, len(got), c.PayloadHex, c.Width)
		}
	}
}
