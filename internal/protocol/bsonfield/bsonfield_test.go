package bsonfield

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

var errSentinel = errors.New("test sentinel")

func marshal(t *testing.T, document bson.D) bson.Raw {
	t.Helper()
	raw, err := bson.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestInt64WidensInt32AndInt64AndRejectsOtherTypes(t *testing.T) {
	tests := []struct {
		name        string
		document    bson.D
		wantValue   int64
		wantPresent bool
		wantErr     bool
	}{
		{name: "int32", document: bson.D{{Key: "n", Value: int32(-7)}}, wantValue: -7, wantPresent: true},
		{name: "int64", document: bson.D{{Key: "n", Value: int64(math.MaxInt64)}}, wantValue: math.MaxInt64, wantPresent: true},
		{name: "missing", document: bson.D{{Key: "other", Value: int64(1)}}},
		{name: "double", document: bson.D{{Key: "n", Value: 1.0}}, wantErr: true},
		{name: "string", document: bson.D{{Key: "n", Value: "1"}}, wantErr: true},
		{name: "bool", document: bson.D{{Key: "n", Value: true}}, wantErr: true},
		{name: "null", document: bson.D{{Key: "n", Value: nil}}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, present, err := Reader{Err: errSentinel}.Int64(marshal(t, test.document), "n")
			if test.wantErr {
				if !errors.Is(err, errSentinel) || present || value != 0 {
					t.Fatalf("Int64 = %d, %v, %v; want type error", value, present, err)
				}
				return
			}
			if err != nil || present != test.wantPresent || value != test.wantValue {
				t.Fatalf("Int64 = %d, %v, %v; want %d, %v, nil", value, present, err, test.wantValue, test.wantPresent)
			}
		})
	}
}

func TestInt64ExactRejectsInt32(t *testing.T) {
	reader := Reader{Err: errSentinel}
	value, present, err := reader.Int64Exact(marshal(t, bson.D{{Key: "n", Value: int64(9)}}), "n")
	if err != nil || !present || value != 9 {
		t.Fatalf("Int64Exact(int64) = %d, %v, %v", value, present, err)
	}
	_, present, err = reader.Int64Exact(marshal(t, bson.D{{Key: "n", Value: int32(9)}}), "n")
	if !errors.Is(err, errSentinel) || present {
		t.Fatalf("Int64Exact(int32) = %v, %v; want type error", present, err)
	}
	_, present, err = reader.Int64Exact(marshal(t, bson.D{}), "n")
	if err != nil || present {
		t.Fatalf("Int64Exact(missing) = %v, %v", present, err)
	}
}

func TestInt32RejectsValuesOutsideInt32Range(t *testing.T) {
	tests := []struct {
		name      string
		value     any
		wantValue int32
		wantErr   bool
	}{
		{name: "int32 min", value: int32(math.MinInt32), wantValue: math.MinInt32},
		{name: "int64 max int32", value: int64(math.MaxInt32), wantValue: math.MaxInt32},
		{name: "int64 above range", value: int64(math.MaxInt32) + 1, wantErr: true},
		{name: "int64 below range", value: int64(math.MinInt32) - 1, wantErr: true},
		{name: "double", value: 1.0, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, present, err := Reader{Err: errSentinel}.Int32(marshal(t, bson.D{{Key: "n", Value: test.value}}), "n")
			if test.wantErr {
				if !errors.Is(err, errSentinel) || present {
					t.Fatalf("Int32 = %d, %v, %v; want error", value, present, err)
				}
				return
			}
			if err != nil || !present || value != test.wantValue {
				t.Fatalf("Int32 = %d, %v, %v; want %d", value, present, err, test.wantValue)
			}
		})
	}
}

func TestStringAndBool(t *testing.T) {
	reader := Reader{Err: errSentinel}
	raw := marshal(t, bson.D{
		{Key: "s", Value: "hello"},
		{Key: "empty", Value: ""},
		{Key: "b", Value: false},
		{Key: "n", Value: int32(1)},
	})
	if value, present, err := reader.String(raw, "s"); err != nil || !present || value != "hello" {
		t.Fatalf("String(s) = %q, %v, %v", value, present, err)
	}
	if value, present, err := reader.String(raw, "empty"); err != nil || !present || value != "" {
		t.Fatalf("String(empty) = %q, %v, %v", value, present, err)
	}
	if _, present, err := reader.String(raw, "n"); !errors.Is(err, errSentinel) || present {
		t.Fatalf("String(n) = %v, %v; want type error", present, err)
	}
	if value, present, err := reader.Bool(raw, "b"); err != nil || !present || value {
		t.Fatalf("Bool(b) = %v, %v, %v", value, present, err)
	}
	if _, present, err := reader.Bool(raw, "n"); !errors.Is(err, errSentinel) || present {
		t.Fatalf("Bool(n) = %v, %v; want type error", present, err)
	}
}

func TestArrays(t *testing.T) {
	reader := Reader{Err: errSentinel}
	raw := marshal(t, bson.D{
		{Key: "ints", Value: bson.A{int32(1), int64(2)}},
		{Key: "mixedInts", Value: bson.A{int64(1), 2.0}},
		{Key: "strings", Value: bson.A{"a", "b"}},
		{Key: "mixedStrings", Value: bson.A{"a", int32(1)}},
		{Key: "empty", Value: bson.A{}},
		{Key: "notArray", Value: "x"},
	})
	if values, present, err := reader.Int64s(raw, "ints"); err != nil || !present || !reflect.DeepEqual(values, []int64{1, 2}) {
		t.Fatalf("Int64s(ints) = %v, %v, %v", values, present, err)
	}
	if _, present, err := reader.Int64s(raw, "mixedInts"); !errors.Is(err, errSentinel) || present {
		t.Fatalf("Int64s(mixedInts) = %v, %v; want element error", present, err)
	}
	if values, present, err := reader.Strings(raw, "strings"); err != nil || !present || !reflect.DeepEqual(values, []string{"a", "b"}) {
		t.Fatalf("Strings(strings) = %v, %v, %v", values, present, err)
	}
	if _, present, err := reader.Strings(raw, "mixedStrings"); !errors.Is(err, errSentinel) || present {
		t.Fatalf("Strings(mixedStrings) = %v, %v; want element error", present, err)
	}
	if values, present, err := reader.Int64s(raw, "empty"); err != nil || !present || values == nil || len(values) != 0 {
		t.Fatalf("Int64s(empty) = %#v, %v, %v; want empty non-nil slice", values, present, err)
	}
	if _, present, err := reader.Strings(raw, "notArray"); !errors.Is(err, errSentinel) || present {
		t.Fatalf("Strings(notArray) = %v, %v; want type error", present, err)
	}
	if values, present, err := reader.Int64s(raw, "missing"); err != nil || present || values != nil {
		t.Fatalf("Int64s(missing) = %v, %v, %v", values, present, err)
	}
}

func TestDocumentReturnsCopy(t *testing.T) {
	reader := Reader{Err: errSentinel}
	raw := marshal(t, bson.D{
		{Key: "doc", Value: bson.D{{Key: "x", Value: int32(1)}}},
		{Key: "notDoc", Value: bson.A{int32(1)}},
	})
	document, present, err := reader.Document(raw, "doc")
	if err != nil || !present {
		t.Fatalf("Document(doc) = %v, %v", present, err)
	}
	for i := range raw {
		raw[i] = 0
	}
	if value, ok := Int64Value(document.Lookup("x")); !ok || value != 1 {
		t.Fatalf("document aliases its source: x = %d, %v", value, ok)
	}
	if _, present, err := reader.Document(marshal(t, bson.D{{Key: "notDoc", Value: bson.A{int32(1)}}}), "notDoc"); !errors.Is(err, errSentinel) || present {
		t.Fatalf("Document(notDoc) = %v, %v; want type error", present, err)
	}
}

func TestFirstPresentKeyWins(t *testing.T) {
	raw := marshal(t, bson.D{
		{Key: "legacy", Value: int64(2)},
		{Key: "current", Value: nil},
	})
	strict := Reader{Err: errSentinel}
	if _, _, err := strict.Int64(raw, "current", "legacy"); !errors.Is(err, errSentinel) {
		t.Fatalf("strict Int64 with null first key = %v; want type error", err)
	}
	if value, present, err := strict.Int64(raw, "missing", "legacy"); err != nil || !present || value != 2 {
		t.Fatalf("strict Int64 falls through missing key = %d, %v, %v", value, present, err)
	}
	nullAbsent := Reader{Err: errSentinel, NullAbsent: true}
	if value, present, err := nullAbsent.Int64(raw, "current", "legacy"); err != nil || !present || value != 2 {
		t.Fatalf("NullAbsent Int64 falls through null key = %d, %v, %v", value, present, err)
	}
	if _, present, err := nullAbsent.Int64(raw, "current"); err != nil || present {
		t.Fatalf("NullAbsent Int64(null) = %v, %v; want absent", present, err)
	}
	undefined := marshal(t, bson.D{{Key: "u", Value: bson.Undefined{}}})
	if _, present, err := nullAbsent.String(undefined, "u"); err != nil || present {
		t.Fatalf("NullAbsent String(undefined) = %v, %v; want absent", present, err)
	}
}

func TestInt64OrZeroIgnoresAbsenceAndWrongTypes(t *testing.T) {
	raw := marshal(t, bson.D{
		{Key: "int32", Value: int32(5)},
		{Key: "int64", Value: int64(6)},
		{Key: "string", Value: "7"},
		{Key: "double", Value: 8.0},
		{Key: "null", Value: nil},
	})
	for key, want := range map[string]int64{"int32": 5, "int64": 6, "string": 0, "double": 0, "null": 0, "missing": 0} {
		if got := Int64OrZero(raw, key); got != want {
			t.Fatalf("Int64OrZero(%s) = %d; want %d", key, got, want)
		}
	}
}

func TestZeroReaderWrapsErrType(t *testing.T) {
	_, _, err := Reader{}.Int64(marshal(t, bson.D{{Key: "n", Value: "x"}}), "n")
	if !errors.Is(err, ErrType) {
		t.Fatalf("zero Reader error = %v; want ErrType", err)
	}
}

func TestShapeDescribesKeysAndTypesWithoutValues(t *testing.T) {
	raw, err := bson.Marshal(bson.D{{Key: "k", Value: "secret-upload-key"}, {Key: "p", Value: int32(9282)},
		{Key: "nested", Value: bson.D{{Key: "vh", Value: "host.example"}}}})
	if err != nil {
		t.Fatal(err)
	}
	got := Shape(raw)
	if got != "{k:string p:int32 nested:{vh:string}}" {
		t.Fatalf("Shape = %q", got)
	}
	if Shape(bson.Raw{1, 2}) != "invalid" {
		t.Fatal("invalid document was described")
	}
}
