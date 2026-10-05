package sessionlogin

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// productionBSONScalarValue supplies the explicit Go type corresponding to a
// reviewed Objective-C scalar branch. It deliberately has no fallback for
// unreviewed encodings; those remain fixture-level rejection cases.
func productionBSONScalarValue(c bsonScalarCase) (any, bool, error) {
	switch c.ObjCType {
	case "B", "c":
		value, err := strconv.ParseBool(c.Value)
		return value, true, err
	case "d":
		value, err := strconv.ParseFloat(c.Value, 64)
		return value, true, err
	case "i":
		value, err := strconv.ParseInt(c.Value, 10, 32)
		return int32(value), true, err
	case "q":
		value, err := strconv.ParseInt(c.Value, 10, 64)
		return value, true, err
	case "null":
		return nil, true, nil
	default:
		return nil, false, nil
	}
}

func TestProductionBSONScalarFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "reconnect", "rc-q5-bson-scalars.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture bsonScalarFixture
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range fixture.Cases {
		value, supported, err := productionBSONScalarValue(testCase)
		if err != nil {
			t.Fatalf("%s: parse typed scalar: %v", testCase.Name, err)
		}
		if !supported {
			if !testCase.ExpectedReject {
				t.Errorf("%s: unsupported encoding lacks expected_reject", testCase.Name)
			}
			t.Logf("%s: skip unreviewed Objective-C encoding %q", testCase.Name, testCase.ObjCType)
			continue
		}
		wire, err := bson.Marshal(bson.D{{Key: "value", Value: value}})
		if err != nil {
			t.Fatalf("%s: production BSON marshal: %v", testCase.Name, err)
		}
		raw := bson.Raw(wire).Lookup("value")
		want, err := hex.DecodeString(testCase.PayloadHex)
		if err != nil {
			t.Fatalf("%s: payload fixture: %v", testCase.Name, err)
		}
		if byte(raw.Type) != testCase.BSONType || !bytes.Equal(raw.Value, want) || len(raw.Value) != testCase.Width {
			t.Errorf("%s: type=%d payload=%x width=%d want type=%d payload=%s width=%d", testCase.Name, raw.Type, raw.Value, len(raw.Value), testCase.BSONType, testCase.PayloadHex, testCase.Width)
		}
	}
}

func TestProductionBSONDocumentFraming(t *testing.T) {
	empty, err := bson.Marshal(bson.D{})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 5 || !bytes.Equal(empty, []byte{5, 0, 0, 0, 0}) {
		t.Fatalf("empty document=%x, want standard five-byte document", empty)
	}
	nested, err := bson.Marshal(bson.D{{Key: "nested", Value: bson.D{}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(nested) < 5 || nested[len(nested)-1] != 0 {
		t.Fatalf("nested document lacks terminal zero: %x", nested)
	}
	declared := int(binary.LittleEndian.Uint32(nested[:4]))
	if declared != len(nested) {
		t.Fatalf("outer BSON length=%d, actual=%d", declared, len(nested))
	}
	value := bson.Raw(nested).Lookup("nested")
	if value.Type != bson.TypeEmbeddedDocument || len(value.Value) != 5 || !bytes.Equal(value.Value, []byte{5, 0, 0, 0, 0}) {
		t.Fatalf("nested empty value=%v/%x", value.Type, value.Value)
	}
}
