package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"maunium.net/go/mautrix/appservice"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
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
		state := &historySendState{}
		state.set(transaction)
		ctx := context.WithValue(t.Context(), historyDeliveryKey, state)
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
	state := &historySendState{}
	state.set(first)
	ctx := context.WithValue(t.Context(), historyDeliveryKey, state)
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

// SDK discard-megolm-session and set-pl use these concrete assertions;
// changing either dynamic type makes the built-in command panic.
func TestHistoryAdapterPreservesBuiltInSDKCommandTypes(t *testing.T) {
	base := &matrix.Connector{AS: appservice.Create()}
	br := &bridgev2.Bridge{Matrix: base, Bot: base.BotIntent()}
	installHistoryMatrix(br)
	if _, ok := br.Matrix.(*matrix.Connector); !ok {
		t.Errorf("SDK crypto command connector assertion would panic: %T", br.Matrix)
	}
	if _, ok := br.Bot.(*matrix.ASIntent); !ok {
		t.Errorf("SDK power-level command bot assertion would panic: %T", br.Bot)
	}
}

type historyEncryptionProbe struct {
	matrix.Crypto
	sawMarker bool
	sawOther  bool
	calls     int
}

func TestHistoricalMultipartConversionStopsBeforeDelivery(t *testing.T) {
	kc, _ := newHistoryTest(t)
	p, err := kc.login.Bridge.GetPortalByMXID(t.Context(), "!selected:test")
	if err != nil {
		t.Fatal(err)
	}
	original := newMessage(kc.messageMeta(5000, 103, 2000, 1700000000), makeMessageID(5000, 103), "synthetic", func(ctx context.Context, p *bridgev2.Portal, intent bridgev2.MatrixAPI, body string) (*bridgev2.ConvertedMessage, error) {
		converted, err := convertNotice(ctx, p, intent, body)
		if err == nil {
			converted.Parts = append(converted.Parts, converted.Parts[0])
		}
		return converted, err
	})
	historical := &historyMessage{RemoteMessage: original, ctx: t.Context()}
	converted, err := historical.ConvertMessage(historical.MutateContext(t.Context()), p, kc.login.Bridge.Bot)
	if err == nil || converted != nil {
		t.Fatal("multipart history could enter the SDK's partial-delivery path")
	}
}

func (p *historyEncryptionProbe) Encrypt(_ context.Context, _ id.RoomID, _ event.Type, content *event.Content) error {
	p.calls++
	_, p.sawMarker = content.Raw[historyPartMarker]
	p.sawOther = content.Raw["synthetic_extra"] == true
	content.Raw = nil
	return nil
}
func TestHistoryCryptoSelectsPartAndRemovesPrivateMarkerBeforeDelegation(t *testing.T) {
	state := &historySendState{}
	ctx := context.WithValue(t.Context(), historyDeliveryKey, state)
	probe := &historyEncryptionProbe{}
	crypto := &historyCrypto{Crypto: probe}
	for _, transaction := range []string{"mooo-history-first-part", "mooo-history-second-part"} {
		content := &event.Content{Parsed: &event.MessageEventContent{MsgType: event.MsgText, Body: "synthetic"}, Raw: map[string]any{historyPartMarker: transaction, "synthetic_extra": true}}
		if err := crypto.Encrypt(ctx, "!selected:test", event.EventMessage, content); err != nil {
			t.Fatal(err)
		}
		if state.get() != transaction || probe.sawMarker || !probe.sawOther {
			t.Fatal("part identity was lost or exposed to encryption")
		}
	}
	if probe.calls != 2 {
		t.Fatal("SDK encryption was not delegated")
	}
	if err := crypto.Encrypt(ctx, "!selected:test", event.EventMessage, &event.Content{}); err == nil || probe.calls != 2 {
		t.Fatal("unidentified historical send reached encryption")
	}
}
func TestHistoryPlaintextTransportRemovesMarkerAndPreservesRequest(t *testing.T) {
	const transaction = "mooo-history-selected-part"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/"+transaction) || r.URL.Query().Get("user_id") != "@sender:test" || r.Header.Get("X-Synthetic") != "retained" {
			t.Error("request identity or options changed")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if _, present := payload[historyPartMarker]; present || payload["body"] != "synthetic plain history" {
			t.Error("private marker leaked or message changed")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	state := &historySendState{}
	ctx := context.WithValue(t.Context(), historyDeliveryKey, state)
	data, err := json.Marshal(map[string]any{"body": "synthetic plain history", "msgtype": "m.text", historyPartMarker: transaction})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, server.URL+"/_matrix/client/v3/rooms/!selected:test/send/m.room.message/random?user_id=%40sender%3Atest", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Synthetic", "retained")
	resp, err := (&http.Client{Transport: newHistoryTransport(http.DefaultTransport)}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if state.get() != transaction {
		t.Fatal("plaintext part identity not selected")
	}
}
