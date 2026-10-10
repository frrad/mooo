package registration

import (
	"errors"
	"net/http"
	"testing"

	"github.com/frrad/mooo/internal/protocol/macweb"
)

func TestApplyMacClientHeadersCurrentSyntheticVector(t *testing.T) {
	request, err := http.NewRequest(http.MethodPost, "https://example.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = ApplyMacClientHeaders(request, macweb.Profile{
		AppVersion: "26.8.0",
		OSVersion:  "26.6.2",
		Language:   "en",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"A":               "mac/26.8.0/en",
		"Accept-Language": "en",
		"Content-Type":    "application/json",
		"User-Agent":      "KT/26.8.0 Mc/26.6.2 en",
	}
	if len(request.Header) != len(want) {
		t.Fatalf("headers = %#v", request.Header)
	}
	for name, value := range want {
		if got := request.Header.Get(name); got != value {
			t.Fatalf("%s = %q, want %q", name, got, value)
		}
	}
}

func TestApplyMacClientHeadersRejectsUnsafeParts(t *testing.T) {
	for _, profile := range []macweb.Profile{
		{},
		{AppVersion: "26.8.0\r\nX: bad", OSVersion: "26.6.2", Language: "en"},
		{AppVersion: "26.8.0", OSVersion: "26.6.2", Language: "en/US"},
	} {
		request, _ := http.NewRequest(http.MethodPost, "https://example.invalid", nil)
		if err := ApplyMacClientHeaders(request, profile); !errors.Is(err, ErrInvalidMacClientProfile) {
			t.Fatalf("error = %v", err)
		}
	}
}
