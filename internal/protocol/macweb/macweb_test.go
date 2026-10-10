package macweb

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

func respond(status int, body string) Doer {
	return doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
}

func newRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.invalid/", nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestApplyHeadersSyntheticVector(t *testing.T) {
	profile := Profile{AppVersion: "26.8.0", OSVersion: "26.6.2", Language: "en", AccessToken: "synthetic-token", DeviceUUID: "synthetic-device"}

	authorized := newRequest(t)
	ApplyHeaders(authorized, profile, true)
	want := map[string]string{
		"A":               "mac/26.8.0/en",
		"Accept-Language": "en",
		"User-Agent":      "KT/26.8.0 Mc/26.6.2 en",
		"Authorization":   "synthetic-token-synthetic-device",
	}
	if len(authorized.Header) != len(want) {
		t.Fatalf("headers = %v", authorized.Header)
	}
	for name, value := range want {
		if got := authorized.Header.Get(name); got != value {
			t.Fatalf("%s = %q, want %q", name, got, value)
		}
	}

	anonymous := newRequest(t)
	ApplyHeaders(anonymous, profile, false)
	if anonymous.Header.Get("Authorization") != "" || len(anonymous.Header) != 3 {
		t.Fatalf("unauthorized headers = %v", anonymous.Header)
	}
}

func TestProfileValidate(t *testing.T) {
	valid := Profile{AppVersion: "26.8.0", OSVersion: "26.6.2", Language: "en", AccessToken: "token", DeviceUUID: "device"}
	if err := valid.Validate(true); err != nil {
		t.Fatal(err)
	}
	noCredentials := Profile{AppVersion: "26.8.0", OSVersion: "26.6.2", Language: "en"}
	if err := noCredentials.Validate(false); err != nil {
		t.Fatalf("unauthenticated profile rejected: %v", err)
	}
	if err := noCredentials.Validate(true); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("missing credentials accepted: %v", err)
	}
	for name, profile := range map[string]Profile{
		"blank language":  {AppVersion: "26.8.0", OSVersion: "26.6.2", Language: " ", AccessToken: "token", DeviceUUID: "device"},
		"NUL token":       {AppVersion: "26.8.0", OSVersion: "26.6.2", Language: "en", AccessToken: "to\x00ken", DeviceUUID: "device"},
		"invalid UTF-8":   {AppVersion: "26.8.0", OSVersion: "\xff", Language: "en", AccessToken: "token", DeviceUUID: "device"},
		"oversized value": {AppVersion: "26.8.0", OSVersion: "26.6.2", Language: "en", AccessToken: strings.Repeat("a", 16385), DeviceUUID: "device"},
	} {
		if err := profile.Validate(true); !errors.Is(err, ErrInvalidProfile) {
			t.Fatalf("%s: error = %v", name, err)
		}
	}
}

func TestDoAcceptsAny2xx(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNoContent, 299} {
		body, err := Do(respond(status, `{"ok":true}`), newRequest(t), 1024)
		if err != nil || string(body) != `{"ok":true}` {
			t.Fatalf("status %d: body=%q err=%v", status, body, err)
		}
	}
}

func TestDoRejectsNon2xxWithCode(t *testing.T) {
	_, err := Do(respond(http.StatusUnauthorized, `{}`), newRequest(t), 1024)
	var statusErr *StatusError
	if !errors.Is(err, ErrStatus) || !errors.As(err, &statusErr) || statusErr.Code != http.StatusUnauthorized {
		t.Fatalf("error = %v", err)
	}
}

func TestDoChecksSizeBeforeStatus(t *testing.T) {
	_, err := Do(respond(http.StatusInternalServerError, strings.Repeat("x", 11)), newRequest(t), 10)
	if !errors.Is(err, ErrResponseTooLarge) || errors.Is(err, ErrStatus) {
		t.Fatalf("error = %v", err)
	}
	body, err := Do(respond(http.StatusOK, strings.Repeat("x", 10)), newRequest(t), 10)
	if err != nil || len(body) != 10 {
		t.Fatalf("body at limit: len=%d err=%v", len(body), err)
	}
}

func TestDoWrapsTransportFailure(t *testing.T) {
	cause := errors.New("synthetic dial failure")
	doer := doerFunc(func(*http.Request) (*http.Response, error) { return nil, cause })
	_, err := Do(doer, newRequest(t), 1024)
	if !errors.Is(err, ErrTransport) || !errors.Is(err, cause) {
		t.Fatalf("error = %v", err)
	}
}

func TestDoRejectsMissingResponse(t *testing.T) {
	doer := doerFunc(func(*http.Request) (*http.Response, error) { return &http.Response{StatusCode: http.StatusOK}, nil })
	if _, err := Do(doer, newRequest(t), 1024); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeJSONObject(t *testing.T) {
	var object map[string]int
	if err := DecodeJSONObject([]byte(" {\"a\":1}\n"), &object); err != nil || object["a"] != 1 {
		t.Fatalf("object=%v err=%v", object, err)
	}
	for _, body := range []string{``, `null`, `[]`, `1`, `{"a":1} {}`, `{"a":1}x`, `{"a":`} {
		var target map[string]int
		if err := DecodeJSONObject([]byte(body), &target); !errors.Is(err, ErrInvalidJSON) {
			t.Fatalf("accepted %q: %v", body, err)
		}
	}
}
