package loco

import (
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestStatusReadsBSONStatusNotHeader(t *testing.T) {
	body, err := bson.Marshal(bson.D{{Key: "status", Value: int32(-328)}})
	if err != nil {
		t.Fatal(err)
	}
	status, err := Status(Packet{Header: Header{Status: 0}, Body: body})
	if err != nil || status != -328 {
		t.Fatalf("Status = (%d, %v), want (-328, nil)", status, err)
	}
}

func TestStatusAcceptsInt64WithinInt32Range(t *testing.T) {
	body, err := bson.Marshal(bson.D{{Key: "status", Value: int64(-950)}})
	if err != nil {
		t.Fatal(err)
	}
	status, err := Status(Packet{Body: body})
	if err != nil || status != -950 {
		t.Fatalf("Status = (%d, %v), want (-950, nil)", status, err)
	}
}

func TestStatusRejectsMissingOrMalformedStatus(t *testing.T) {
	bodies := map[string]bson.D{
		"missing":      {{Key: "other", Value: int32(0)}},
		"string":       {{Key: "status", Value: "0"}},
		"double":       {{Key: "status", Value: 0.0}},
		"out of range": {{Key: "status", Value: int64(1) << 40}},
	}
	for name, doc := range bodies {
		body, err := bson.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Status(Packet{Body: body}); !errors.Is(err, ErrMissingStatus) {
			t.Errorf("%s: Status error = %v, want ErrMissingStatus", name, err)
		}
	}
	if _, err := Status(Packet{}); !errors.Is(err, ErrMissingStatus) {
		t.Errorf("empty body: Status error = %v, want ErrMissingStatus", err)
	}
}
