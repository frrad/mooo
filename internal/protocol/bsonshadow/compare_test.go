package bsonshadow

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func document(elements ...[]byte) []byte {
	size := 5
	for _, element := range elements {
		size += len(element)
	}
	out := make([]byte, 4, size)
	binary.LittleEndian.PutUint32(out, uint32(size))
	for _, element := range elements {
		out = append(out, element...)
	}
	return append(out, 0)
}

func element(typ byte, key string, payload []byte) []byte {
	out := []byte{typ}
	out = append(out, key...)
	out = append(out, 0)
	return append(out, payload...)
}

func stringPayload(value string) []byte {
	out := make([]byte, 4, 4+len(value)+1)
	binary.LittleEndian.PutUint32(out, uint32(len(value)+1))
	out = append(out, value...)
	return append(out, 0)
}

func int32Payload(value int32) []byte {
	return binary.LittleEndian.AppendUint32(nil, uint32(value))
}

func kinds(report Report) []Kind {
	var out []Kind
	for _, discrepancy := range report.Discrepancies {
		out = append(out, discrepancy.Kind)
	}
	return out
}

func requireSingle(t *testing.T, report Report, kind Kind, path string) Discrepancy {
	t.Helper()
	if len(report.Discrepancies) != 1 {
		t.Fatalf("discrepancies = %+v, want exactly one %s at %s", report.Discrepancies, kind, path)
	}
	got := report.Discrepancies[0]
	if got.Kind != kind || got.Path != path {
		t.Fatalf("discrepancy = %+v, want %s at %s", got, kind, path)
	}
	return got
}

func TestCompareAgreesOnOrdinaryDocument(t *testing.T) {
	body, err := bson.Marshal(bson.D{
		{Key: "status", Value: int32(0)},
		{Key: "chatId", Value: int64(1234567890123)},
		{Key: "message", Value: "hello"},
		{Key: "ratio", Value: 0.5},
		{Key: "read", Value: true},
		{Key: "nested", Value: bson.D{{Key: "a", Value: "b"}}},
		{Key: "list", Value: bson.A{int64(1), "two", bson.D{{Key: "three", Value: int32(3)}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	report := Compare(body)
	if len(report.Discrepancies) != 0 {
		t.Fatalf("discrepancies = %+v, want none", report.Discrepancies)
	}
}

func TestCompareTreatsMongoNullForAbsentKeyAsAbsent(t *testing.T) {
	// Struct decoding gives a null field its zero value, which is the same
	// observable state as an absent key, so this is not a discrepancy.
	body := document(element(0x0a, "nothing", nil))
	report := Compare(body)
	if len(report.Discrepancies) != 0 {
		t.Fatalf("discrepancies = %+v, want none", report.Discrepancies)
	}
}

func TestCompareReportsInvalidUTF8ValueDroppedByOfficial(t *testing.T) {
	body := document(
		element(0x02, "r", stringPayload("\xc3(")),
		element(0x02, "pr", stringPayload("42")),
	)
	got := requireSingle(t, Compare(body), KindOfficialDroppedInvalidUTF8, "r")
	if got.Mongo != "string(len=2)" || got.Official != "absent" {
		t.Fatalf("descriptors = %q / %q", got.Mongo, got.Official)
	}
}

func TestCompareReportsDuplicateWhereLaterInvalidValueOverwrites(t *testing.T) {
	body := document(
		element(0x02, "field", stringPayload("7")),
		element(0x02, "field", stringPayload("\xc3(")),
	)
	requireSingle(t, Compare(body), KindValueDiffers, "field")
}

func TestCompareReportsLaterNullResettingEarlierValue(t *testing.T) {
	body := document(
		element(0x02, "field", stringPayload("7")),
		element(0x0a, "field", nil),
	)
	got := requireSingle(t, Compare(body), KindNullOverwrites, "field")
	if got.Mongo != "null" || got.Official != "string(len=1)" {
		t.Fatalf("descriptors = %q / %q", got.Mongo, got.Official)
	}
}

func TestCompareReportsStringTruncatedAtNul(t *testing.T) {
	body := document(element(0x02, "field", stringPayload("42\x00tail")))
	requireSingle(t, Compare(body), KindStringNulTruncated, "field")
}

func TestCompareReportsInvalidKeyRejectedOnlyByOfficial(t *testing.T) {
	body := document(element(0x02, "\xc3(", stringPayload("42")))
	report := Compare(body)
	requireSingle(t, report, KindOfficialRejects, "")
	if report.OfficialErr == nil || report.MongoErr != nil {
		t.Fatalf("errors = mongo %v / official %v", report.MongoErr, report.OfficialErr)
	}
}

func TestCompareReportsTypeOfficialIgnores(t *testing.T) {
	body := document(element(0x09, "sentAt", binary.LittleEndian.AppendUint64(nil, 1700000000000)))
	got := requireSingle(t, Compare(body), KindOfficialIgnoresType, "sentAt")
	if got.Mongo != "datetime" || got.Official != "absent" {
		t.Fatalf("descriptors = %q / %q", got.Mongo, got.Official)
	}
}

func TestCompareReportsOfficialPartialOnUnknownType(t *testing.T) {
	decimal := make([]byte, 16)
	body := document(
		element(0x02, "before", stringPayload("x")),
		element(0x13, "amount", decimal),
		element(0x02, "after", stringPayload("y")),
	)
	report := Compare(body)
	got := kinds(report)
	want := []Kind{KindOfficialPartial, KindMongoOnly, KindMongoOnly}
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", got, want)
		}
	}
	if report.Discrepancies[0].Official != "unknown-type(0x13)" {
		t.Fatalf("partial descriptor = %q", report.Discrepancies[0].Official)
	}
}

func TestCompareReportsArrayNullSkippedByOfficial(t *testing.T) {
	array := document(
		element(0x10, "0", int32Payload(1)),
		element(0x0a, "1", nil),
		element(0x10, "2", int32Payload(3)),
	)
	body := document(element(0x04, "ids", array))
	report := Compare(body)
	if len(report.Discrepancies) == 0 || report.Discrepancies[0].Kind != KindArrayLength || report.Discrepancies[0].Path != "ids" {
		t.Fatalf("discrepancies = %+v, want array-length at ids first", report.Discrepancies)
	}
}

func TestCompareReportsTypeDiffersForNonCanonicalBool(t *testing.T) {
	body := document(element(0x08, "flag", []byte{2}))
	report := Compare(body)
	if len(report.Discrepancies) != 1 {
		t.Fatalf("discrepancies = %+v, want one", report.Discrepancies)
	}
	// Either mongo rejects the byte or decodes it differently; both are
	// discrepancies the official nonzero-is-true rule exposes.
	if kind := report.Discrepancies[0].Kind; kind != KindMongoRejects && kind != KindValueDiffers {
		t.Fatalf("kind = %s", kind)
	}
}

func TestCompareNoDiscrepancyWhenBothReject(t *testing.T) {
	report := Compare([]byte{1, 2})
	if len(report.Discrepancies) != 0 || report.MongoErr == nil || report.OfficialErr == nil {
		t.Fatalf("report = %+v, want both errors and no discrepancy", report)
	}
}

func TestCompareFloatNaNIsEqual(t *testing.T) {
	body := document(element(0x01, "x", binary.LittleEndian.AppendUint64(nil, math.Float64bits(math.NaN()))))
	if report := Compare(body); len(report.Discrepancies) != 0 {
		t.Fatalf("discrepancies = %+v", report.Discrepancies)
	}
}

func TestComparePathsNeverContainValuesAndHashUnsafeKeys(t *testing.T) {
	secret := "private message text"
	body := document(
		element(0x03, "1234567890", document(element(0x02, "body", stringPayload(secret+"\x00x")))),
	)
	report := Compare(body)
	if len(report.Discrepancies) != 1 {
		t.Fatalf("discrepancies = %+v", report.Discrepancies)
	}
	got := report.Discrepancies[0]
	if !strings.HasPrefix(got.Path, "<key:") || !strings.HasSuffix(got.Path, ">.body") {
		t.Fatalf("path = %q, want hashed numeric key then .body", got.Path)
	}
	for _, field := range []string{got.Path, got.Mongo, got.Official, string(got.Kind)} {
		if strings.Contains(field, "private") || strings.Contains(field, "1234567890") {
			t.Fatalf("discrepancy leaks content: %+v", got)
		}
	}
}
