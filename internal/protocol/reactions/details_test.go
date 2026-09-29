package reactions

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestNewDetailsHTTPRequest(t *testing.T) {
	profile := testProfile()
	profile.UserID = 7
	req, err := NewDetailsHTTPRequest(t.Context(), profile, 42, 88, 99)
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != http.MethodPost || req.URL.String() != BaseURL+detailsPath {
		t.Fatalf("request = %s %s", req.Method, req.URL)
	}
	for key, want := range map[string]string{
		"Accept": "application/json", "Content-Type": "application/json; charset=utf-8",
		"talk-agent": "macos/26.8.0", "talk-user-id": "7", "talk-language": "en",
	} {
		if got := req.Header.Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.Number
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 3 || payload["chatId"].String() != "42" || payload["linkId"].String() != "88" || payload["logId"].String() != "99" {
		t.Fatalf("payload = %#v", payload)
	}

	direct, err := NewDetailsHTTPRequest(t.Context(), profile, 42, 0, 99)
	if err != nil {
		t.Fatal(err)
	}
	directBody, _ := io.ReadAll(direct.Body)
	if strings.Contains(string(directBody), "linkId") {
		t.Fatalf("direct-chat payload = %s", directBody)
	}

	profile.UserID = 0
	if _, err := NewDetailsHTTPRequest(t.Context(), profile, 42, 0, 99); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing user id error = %v", err)
	}
}

func TestDecodeDetailsResponse(t *testing.T) {
	response, err := DecodeDetailsResponse([]byte(`{
		"status":0,
		"details":[
			{"k":1,"o":"1","u":["7","invalid","7","0"]},
			{"k":2,"o":"1200509","u":["8"],"itemMeta":{"itemCode":"synthetic-code","name":"synthetic-name","title":"synthetic-title"}},
			{"k":2,"o":"missing-users","u":null}
		],
		"future":{"shape":true}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != 0 || len(response.Details) != 2 || string(response.Fields["future"]) != `{"shape":true}` {
		t.Fatalf("response = %#v", response)
	}
	if got := response.Details[0]; got.Source != DetailSourceMini || got.Kind != 1 || got.ReactionID != "1" || len(got.UserIDs) != 2 || got.UserIDs[0] != 7 || got.UserIDs[1] != 0 {
		t.Fatalf("legacy-compatible mini detail = %#v", got)
	}
	if got := response.Details[1]; got.Kind != 2 || got.ReactionID != "1200509" || got.ItemMeta == nil || got.ItemMeta.ItemCode != "synthetic-code" || got.ItemMeta.Name != "synthetic-name" || got.ItemMeta.Title != "synthetic-title" {
		t.Fatalf("custom detail = %#v", got)
	}

	for _, body := range []string{
		``, `null`, `[]`, `{"details":[]}`, `{"status":0}`, `{"status":0,"details":null}`,
	} {
		if _, err := DecodeDetailsResponse([]byte(body)); !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("body %s error = %v", body, err)
		}
	}
	if response, err := DecodeDetailsResponse([]byte(`{"status":-1,"details":[]}`)); !errors.Is(err, ErrRejected) || response.Status != -1 {
		t.Fatalf("rejected response=%#v err=%v", response, err)
	}
}

func TestMergeDetails(t *testing.T) {
	members := MembersResponse{Members: map[Type][]int64{
		Heart: {7, 8, 7}, Like: {},
	}}
	mini := DetailsResponse{Details: []Detail{
		{Source: DetailSourceMini, Kind: 1, ReactionID: "1", UserIDs: []int64{8, 9}},
		{Source: DetailSourceMini, Kind: 2, ReactionID: "1200509", UserIDs: []int64{10}, ItemMeta: &ItemMeta{ItemCode: "synthetic"}},
		{Source: DetailSourceMini, Kind: 1, ReactionID: "future", UserIDs: []int64{11}},
	}}
	merged := MergeDetails(members, mini)
	if len(merged) != 3 {
		t.Fatalf("merged = %#v", merged)
	}
	if got := merged[0]; got.Source != DetailSourceLegacy || got.ReactionID != "1" || len(got.UserIDs) != 3 || got.UserIDs[0] != 7 || got.UserIDs[1] != 8 || got.UserIDs[2] != 9 {
		t.Fatalf("merged legacy = %#v", got)
	}
	if got := merged[1]; got.Source != DetailSourceMini || got.Kind != 2 || got.ItemMeta == nil {
		t.Fatalf("merged custom = %#v", got)
	}
	if got := merged[2]; got.Source != DetailSourceMini || got.ReactionID != "future" {
		t.Fatalf("unmatched mini = %#v", got)
	}
}
