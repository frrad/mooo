package tokenrefresh

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func testProfile() ClientProfile {
	return ClientProfile{AppVersion: "26.8.0", OSVersion: "26.6.2", Language: "en", AccessToken: "old-access", DeviceUUID: "wire-device"}
}

func TestNewHTTPRequest(t *testing.T) {
	req, err := NewHTTPRequest(context.Background(), testProfile(), Request{RefreshToken: "old-refresh"})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(req.Body)
	if req.Method != http.MethodPost || req.URL.String() != BaseURL+Path || string(body) != "grant_type=refresh_token&refresh_token=old-refresh" {
		t.Fatalf("unexpected request method=%s url=%s body=%q", req.Method, req.URL, body)
	}
	for key, want := range map[string]string{
		"A": "mac/26.8.0/en", "Accept-Language": "en",
		"User-Agent": "KT/26.8.0 Mc/26.6.2 en", "Authorization": "old-access-wire-device",
		"Content-Type": "application/x-www-form-urlencoded; charset=utf-8",
	} {
		if got := req.Header.Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestDecodeResponseRequiresCompleteTriple(t *testing.T) {
	rotation, err := DecodeResponse([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","token_type":"bearer"}`))
	if err != nil || rotation.AccessToken != "new-access" || rotation.RefreshToken != "new-refresh" || rotation.TokenType != "bearer" {
		t.Fatalf("rotation=%v err=%v", rotation, err)
	}
	for _, body := range []string{
		`{}`, `{"access_token":"a","refresh_token":"r"}`,
		`{"access_token":"","refresh_token":"r","token_type":"t"}`,
		`{"status":-950,"access_token":"a","refresh_token":"r","token_type":"t"}`,
		`{"status":"0","access_token":"a","refresh_token":"r","token_type":"t"}`,
		`{"access_token":"a","refresh_token":"r","token_type":"t"} {}`,
	} {
		if _, err := DecodeResponse([]byte(body)); err == nil {
			t.Fatalf("accepted invalid response %q", body)
		}
	}
}

func TestFormattingRedactsRotation(t *testing.T) {
	inventedValue := "invented-value"
	rotation := Rotation{AccessToken: inventedValue, RefreshToken: inventedValue, TokenType: inventedValue}
	if strings.Contains(fmt.Sprintf("%v %#v %+v", rotation, rotation, rotation), inventedValue) {
		t.Fatal("rotation formatting revealed a secret")
	}
}
