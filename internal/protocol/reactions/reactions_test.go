package reactions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func testProfile() ClientProfile {
	return ClientProfile{
		AppVersion: "26.8.0", OSVersion: "26.6.2", Language: "en",
		AccessToken: "synthetic-token", DeviceUUID: "synthetic-device",
	}
}

func TestNewHTTPRequest(t *testing.T) {
	req, err := NewHTTPRequest(t.Context(), testProfile(), Request{
		ChatID: 42, LogID: 99, Type: Check, RequestID: 1234,
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != http.MethodPost || req.URL.String() != BaseURL+"/messaging/chats/42/bubble/reactions" {
		t.Fatalf("request = %s %s", req.Method, req.URL)
	}
	for key, want := range map[string]string{
		"Content-Type": "application/json", "Accept-Language": "en",
		"User-Agent": "KT/26.8.0 Mc/26.6.2 en", "A": "mac/26.8.0/en",
		"Authorization": "synthetic-token-synthetic-device",
	} {
		if got := req.Header.Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 3 || payload["logId"].(json.Number).String() != "99" || payload["type"].(json.Number).String() != "3" || payload["reqId"].(json.Number).String() != "1234" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestNewHTTPRequestIncludesOpenChatLink(t *testing.T) {
	req, err := NewHTTPRequest(t.Context(), testProfile(), Request{
		ChatID: 42, LogID: 99, LinkID: 88, Type: Heart, RequestID: 1234,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(req.Body)
	if !strings.Contains(string(body), `"linkId":88`) {
		t.Fatalf("payload = %s", body)
	}
}

func TestMembersRequestAndResponse(t *testing.T) {
	req, err := NewMembersHTTPRequest(t.Context(), testProfile(), 42, 99)
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != http.MethodGet || req.URL.String() != BaseURL+"/messaging/chats/42/bubble/reactions/99/members" || req.Header.Get("Authorization") == "" {
		t.Fatalf("members request = %s %s headers=%v", req.Method, req.URL, req.Header)
	}
	response, err := DecodeMembersResponse([]byte(`{"1":[7,8],"3":[9],"revision":4}`))
	if err != nil || response.Revision != 4 || len(response.Members[Heart]) != 2 || response.Members[Check][0] != 9 {
		t.Fatalf("members=%#v err=%v", response, err)
	}
	for _, body := range []string{
		`{"1":[7]}`, `{"0":[7],"revision":4}`, `{"7":[7],"revision":4}`,
		`{"1":[0],"revision":4}`, `{"1":null,"revision":4}`,
	} {
		if _, err := DecodeMembersResponse([]byte(body)); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("body %s error = %v", body, err)
		}
	}
}

func TestRequestValidationAndResponse(t *testing.T) {
	for _, request := range []Request{
		{},
		{ChatID: 1, LogID: 2, Type: -1, RequestID: 3},
		{ChatID: 1, LogID: 2, Type: 7, RequestID: 3},
		{ChatID: 1, LogID: 2, Type: Heart},
	} {
		if _, err := NewHTTPRequest(context.Background(), testProfile(), request); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("request %#v error = %v", request, err)
		}
	}
	if response, err := DecodeResponse([]byte(`{"status":0}`)); err != nil || response.Status != 0 {
		t.Fatalf("response=%#v err=%v", response, err)
	}
	if _, err := DecodeResponse([]byte(`{"status":-1}`)); !errors.Is(err, ErrRejected) {
		t.Fatalf("reject error = %v", err)
	}
	if _, err := DecodeResponse([]byte(`{"status":0}{}`)); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("trailing response error = %v", err)
	}
	if _, err := DecodeResponse([]byte(`{}`)); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("missing status error = %v", err)
	}
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }

func TestSendDoesNotRetryTransportFailure(t *testing.T) {
	attempts := 0
	_, err := Send(t.Context(), doerFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		return nil, errors.New("synthetic disconnect")
	}), testProfile(), Request{ChatID: 42, LogID: 99, Type: Heart, RequestID: 1234})
	if err == nil || attempts != 1 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}
}
