package friends

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/protocol/macweb"
)

func TestNewAddByPhoneHTTPRequest(t *testing.T) {
	req, err := NewAddByPhoneHTTPRequest(context.Background(), macweb.Profile{
		AppVersion: "26.8.0", OSVersion: "26.6.2", Language: "en",
		AccessToken: "synthetic-token", DeviceUUID: "synthetic-device",
	}, AddByPhoneRequest{PhoneNumber: "5550101", CountryISO: "us", CountryCode: "1", NickName: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != http.MethodPost || req.URL.String() != BaseURL+AddByPhonePath {
		t.Fatalf("unexpected request %s %s", req.Method, req.URL)
	}
	body, _ := io.ReadAll(req.Body)
	wantBody := "country_code=1&country_iso=US&nickname=Test&phonenumber=5550101&referrer=etc"
	if string(body) != wantBody {
		t.Fatalf("body = %q, want %q", body, wantBody)
	}
	for key, want := range map[string]string{
		"A": "mac/26.8.0/en", "Accept-Language": "en",
		"User-Agent":    "KT/26.8.0 Mc/26.6.2 en",
		"Authorization": "synthetic-token-synthetic-device",
		"Content-Type":  "application/x-www-form-urlencoded; charset=utf-8",
	} {
		if got := req.Header.Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestDecodeAddByPhoneResponse(t *testing.T) {
	response, err := DecodeAddByPhoneResponse([]byte(`{"status":0,"friend":{"userId":123}}`))
	if err != nil || response.Friend.UserID != 123 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	if _, err := DecodeAddByPhoneResponse([]byte(`{"status":0,"friend":{}}`)); err == nil {
		t.Fatal("missing userId accepted")
	}
	if _, err := DecodeAddByPhoneResponse([]byte(`{"status":-1}`)); err == nil || !strings.Contains(err.Error(), "status -1") {
		t.Fatalf("unexpected rejection error: %v", err)
	}
}
