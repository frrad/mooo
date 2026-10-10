package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

func TestHistoricalTransactionSurvivesLostResponseAndRestart(t *testing.T) {
	meta := &database.Message{BridgeID: "synthetic", Room: networkid.PortalKey{ID: "5000", Receiver: "1000"}, ID: "5000:103", PartID: "photo-0"}
	transaction, err := historyTransactionID("!selected:test", "@sender:test", meta, event.EventMessage)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	applied := map[string]string{}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		first := requests == 1
		key := r.URL.Path
		result, exists := applied[key]
		if !exists {
			result = "$one:test"
			applied[key] = result
		}
		mu.Unlock()
		if first {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"event_id": result})
	}))
	defer server.Close()
	send := func(volatile string) (string, error) {
		// A new transport models restart; the original SDK-generated ID can change.
		httpClient := &http.Client{Transport: newHistoryTransport(http.DefaultTransport)}
		ctx := context.WithValue(t.Context(), historyTransactionKey, transaction)
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, server.URL+"/_matrix/client/v3/rooms/!selected:test/send/m.room.encrypted/"+volatile, strings.NewReader(`{"ciphertext":"synthetic"}`))
		if err != nil {
			return "", err
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			return "", err
		}
		defer func() { _ = resp.Body.Close() }()
		var value struct {
			EventID string `json:"event_id"`
		}
		err = json.NewDecoder(resp.Body).Decode(&value)
		return value.EventID, err
	}
	if _, err = send("first-sdk-id"); err == nil {
		t.Fatal("lost acknowledgement not simulated")
	}
	got, err := send("new-sdk-id-after-restart")
	if err != nil || got != "$one:test" {
		t.Fatalf("recovery: %q %v", got, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(applied) != 1 {
		t.Fatalf("restart applied duplicate events: %v", applied)
	}
}
func TestHistoricalTransactionScopesPartsAndLeavesKeyRequestsAlone(t *testing.T) {
	meta := &database.Message{BridgeID: "synthetic", Room: networkid.PortalKey{ID: "5000", Receiver: "1000"}, ID: "5000:103", PartID: "photo-0"}
	first, err := historyTransactionID("!selected:test", "@sender:test", meta, event.EventMessage)
	if err != nil {
		t.Fatal(err)
	}
	meta.PartID = "photo-1"
	second, err := historyTransactionID("!selected:test", "@sender:test", meta, event.EventMessage)
	if err != nil || first == second {
		t.Fatal("multipart transaction alias")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_matrix/client/v3/keys/query" {
			t.Errorf("key endpoint changed: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ctx := context.WithValue(t.Context(), historyTransactionKey, first)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/_matrix/client/v3/keys/query", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: newHistoryTransport(http.DefaultTransport)}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
}
func TestHistoricalMessageContextEndsWithOperatorCall(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	kc, _ := newHistoryTest(t)
	original := newMessage(kc.messageMeta(5000, 103, 2000, 1700000000), makeMessageID(5000, 103), "synthetic", convertNotice)
	wrapped := &historyMessage{RemoteMessage: original, ctx: parent}
	derived := wrapped.MutateContext(context.Background())
	cancel()
	select {
	case <-derived.Done():
	case <-time.After(time.Second):
		t.Fatal("history handling outlived operator context")
	}
}
