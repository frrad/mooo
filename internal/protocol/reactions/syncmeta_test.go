package reactions

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
)

type syncMetaDoer struct {
	request *http.Request
	status  int
	body    []byte
}

func (d *syncMetaDoer) Do(req *http.Request) (*http.Response, error) {
	d.request = req
	return &http.Response{StatusCode: d.status, Body: io.NopCloser(bytes.NewReader(d.body)), Request: req}, nil
}

func observedSyncMetaPage(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../../research/fixtures/reactions/observed-sync-meta-page.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Page json.RawMessage `json:"page"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture.Page
}

func TestFetchSyncMetaRequestAndObservedPage(t *testing.T) {
	profile := ClientProfile{AppVersion: "26.8.0", OSVersion: "15.0", Language: "en", UserID: 7, AccessToken: "token", DeviceUUID: "device"}
	doer := &syncMetaDoer{status: http.StatusOK, body: observedSyncMetaPage(t)}
	page, err := FetchSyncMeta(t.Context(), doer, profile, 3000, 3948377092013952281)
	if err != nil {
		t.Fatal(err)
	}
	u := doer.request.URL
	if doer.request.Method != http.MethodGet || u.Host != "talk-pilsner.kakao.com" || u.Path != "/messaging/chats/3000/chat-log/meta/sync-meta" ||
		u.Query().Get("cur") != "3948377092013952281" || u.Query().Get("max") != "3948377092013952281" || u.Query().Get("cnt") != "0" ||
		doer.request.Header.Get("Authorization") != "token-device" {
		t.Fatalf("request = %s %s %v", doer.request.Method, u, doer.request.Header)
	}
	if !page.Last || len(page.Items) != 2 {
		t.Fatalf("page = %+v", page)
	}
}

func TestFetchSyncMetaRejectsMissingContentAndHTTPFailure(t *testing.T) {
	profile := ClientProfile{AppVersion: "26.8.0", OSVersion: "15.0", Language: "en", UserID: 7, AccessToken: "token", DeviceUUID: "device"}
	for name, doer := range map[string]*syncMetaDoer{
		"no content": {status: http.StatusOK, body: []byte(`{"last":true}`)},
		"http 400":   {status: http.StatusBadRequest, body: []byte(`{"status":400}`)},
		"not json":   {status: http.StatusOK, body: []byte(`nope`)},
	} {
		if _, err := FetchSyncMeta(t.Context(), doer, profile, 3000, 1); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	if _, err := FetchSyncMeta(t.Context(), &syncMetaDoer{status: http.StatusOK, body: []byte(`{"content":[],"last":true}`)}, profile, 3000, 0); err == nil {
		t.Fatal("zero cursor accepted")
	}
}
