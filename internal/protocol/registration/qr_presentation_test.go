package registration

import (
	"errors"
	"strings"
	"testing"
)

func TestParseQRPresentationPreservesPayloadAndExtractsID(t *testing.T) {
	payload := "synthetic://example.invalid/path?id=opaque%20id&other=%26#fragment"
	presentation, err := ParseQRPresentation(payload)
	if err != nil {
		t.Fatal(err)
	}
	if presentation.Payload != payload {
		t.Fatalf("payload = %q, want original %q", presentation.Payload, payload)
	}
	if presentation.ID != "opaque id" {
		t.Fatalf("id = %q, want decoded query value", presentation.ID)
	}
}

func TestParseQRPresentationPreservesRawPlusAndDecodesPercentPlus(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantID  string
	}{
		{name: "raw plus", payload: "synthetic://host?id=a+b", wantID: "a+b"},
		{name: "percent plus", payload: "synthetic://host?id=a%2Bb", wantID: "a+b"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			presentation, err := ParseQRPresentation(test.payload)
			if err != nil {
				t.Fatal(err)
			}
			if presentation.ID != test.wantID {
				t.Fatalf("id = %q, want %q", presentation.ID, test.wantID)
			}
		})
	}
}

func TestParseQRPresentationDoesNotValidateSchemeHostOrOtherFields(t *testing.T) {
	for _, payload := range []string{
		"synthetic://any-host?id=one",
		"not-a-host?id=two",
		"https://example.invalid?id=three&checkKey=unresolved",
	} {
		presentation, err := ParseQRPresentation(payload)
		if err != nil {
			t.Fatalf("ParseQRPresentation(%q): %v", payload, err)
		}
		if presentation.ID == "" {
			t.Fatalf("empty id for %q", payload)
		}
	}
}

func TestParseQRPresentationRejectsMalformedMissingAndAmbiguousID(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{name: "malformed URL escape", body: "synthetic://host?id=%zz", want: ErrInvalidQRPresentation},
		{name: "missing id", body: "synthetic://host?other=value", want: ErrMissingQRID},
		{name: "empty id", body: "synthetic://host?id=", want: ErrMissingQRID},
		{name: "duplicate id", body: "synthetic://host?id=one&id=two", want: ErrAmbiguousQRID},
		{name: "escaped duplicate id", body: "synthetic://host?id=one&%69d=two", want: ErrAmbiguousQRID},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseQRPresentation(test.body)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if strings.Contains(err.Error(), "one") || strings.Contains(err.Error(), "two") || strings.Contains(err.Error(), "zz") {
				t.Fatalf("error leaked payload data: %q", err)
			}
		})
	}
}

func TestParseQRPresentationRetainsShapeBoundsAndUTF8Validation(t *testing.T) {
	for _, payload := range []string{
		"",
		strings.Repeat("x", MaxQRPayloadBytes+1),
		string([]byte{0xff}) + "?id=one",
	} {
		_, err := ParseQRPresentation(payload)
		if !errors.Is(err, ErrInvalidQRPresentation) {
			t.Fatalf("payload validation error = %v, want %v", err, ErrInvalidQRPresentation)
		}
	}
}
