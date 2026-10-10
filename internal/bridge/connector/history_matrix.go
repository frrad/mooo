package connector

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type historyContextKey uint8

const historyDeliveryKey historyContextKey = 0
const historyPartMarker = "com.frrad.mooo.history_transaction"

// This per-event state is populated for each actual converted part, before
// encryption or plaintext HTTP serialization. It never replaces an SDK intent.
type historySendState struct {
	mu          sync.Mutex
	transaction string
}

func (s *historySendState) set(transaction string) {
	s.mu.Lock()
	s.transaction = transaction
	s.mu.Unlock()
}
func (s *historySendState) get() string { s.mu.Lock(); defer s.mu.Unlock(); return s.transaction }

type historyMessage struct {
	bridgev2.RemoteMessage
	ctx context.Context
}

func (h *historyMessage) MutateContext(ctx context.Context) context.Context {
	if original, ok := h.RemoteMessage.(bridgev2.RemoteEventWithContextMutation); ok {
		ctx = original.MutateContext(ctx)
	}
	bounded, cancel := context.WithCancel(ctx)
	context.AfterFunc(h.ctx, cancel)
	return context.WithValue(bounded, historyDeliveryKey, &historySendState{})
}
func (h *historyMessage) ConvertMessage(ctx context.Context, p *bridgev2.Portal, intent bridgev2.MatrixAPI) (*bridgev2.ConvertedMessage, error) {
	if _, ok := ctx.Value(historyDeliveryKey).(*historySendState); !ok {
		return nil, errors.New("connector: historical conversion context is unavailable")
	}
	if p == nil || p.Bridge == nil || intent == nil || h.GetID() == "" {
		return nil, errors.New("connector: historical conversion has no stable identity")
	}
	if as, ok := intent.(*matrix.ASIntent); ok {
		if as.Matrix == nil || as.Matrix.Client == nil || as.Matrix.Client.Client == nil {
			return nil, errors.New("connector: historical Matrix client unavailable")
		}
		if _, ok := as.Matrix.Client.Client.Transport.(*historyTransport); !ok {
			return nil, errors.New("connector: historical external Matrix intent is unsupported")
		}
	}
	converted, err := h.RemoteMessage.ConvertMessage(ctx, p, intent)
	if err != nil || converted == nil {
		return converted, err
	}
	// The SDK deduplicates a source message when any part is already mapped.
	// Reject multiple parts before sending: a partially successful send must
	// not make explicit resume silently skip the remaining parts.
	if len(converted.Parts) != 1 {
		return nil, errors.New("connector: historical conversion requires exactly one part; multipart history is unsupported")
	}
	for _, part := range converted.Parts {
		if part == nil {
			return nil, errors.New("connector: historical conversion contains a nil part")
		}
		transaction, err := historyTransactionID(p.MXID, intent.GetMXID(), &database.Message{BridgeID: p.Bridge.ID, Room: p.PortalKey, ID: h.GetID(), PartID: part.ID}, part.Type)
		if err != nil {
			return nil, err
		}
		if part.Extra == nil {
			part.Extra = make(map[string]any)
		}
		part.Extra[historyPartMarker] = transaction
	}
	return converted, nil
}
func (h *historyMessage) GetTimestamp() time.Time {
	if original, ok := h.RemoteMessage.(bridgev2.RemoteEventWithTimestamp); ok {
		return original.GetTimestamp()
	}
	return time.Time{}
}
func (h *historyMessage) GetStreamOrder() int64 {
	if original, ok := h.RemoteMessage.(bridgev2.RemoteEventWithStreamOrder); ok {
		return original.GetStreamOrder()
	}
	return 0
}
func (*historyMessage) ShouldCreatePortal() bool { return false }

// Keep the concrete Connector and ASIntent types: built-in SDK commands and
// migration helpers assert them. Only the crypto interface and HTTP boundary
// are decorated, preserving the SDK's joining, encryption and send behavior.
func installHistoryMatrix(br *bridgev2.Bridge) {
	base, ok := br.Matrix.(*matrix.Connector)
	if !ok {
		return
	}
	base.AS.HTTPClient.Transport = newHistoryTransport(base.AS.HTTPClient.Transport)
	if base.Crypto != nil {
		if _, ok := base.Crypto.(*historyCrypto); !ok {
			base.Crypto = &historyCrypto{Crypto: base.Crypto}
		}
	}
}

type historyCrypto struct{ matrix.Crypto }

func (c *historyCrypto) Encrypt(ctx context.Context, room id.RoomID, typ event.Type, content *event.Content) error {
	if state, ok := ctx.Value(historyDeliveryKey).(*historySendState); ok {
		transaction, _ := content.Raw[historyPartMarker].(string)
		if transaction == "" {
			return errors.New("connector: historical encrypted send has no stable part identity")
		}
		state.set(transaction)
		delete(content.Raw, historyPartMarker)
	}
	return c.Crypto.Encrypt(ctx, room, typ, content)
}
func historyTransactionID(room id.RoomID, sender id.UserID, meta *database.Message, typ event.Type) (string, error) {
	if meta == nil || meta.ID == "" {
		return "", errors.New("connector: historical send has no stable message identity")
	}
	encoded, err := json.Marshal([]string{string(room), string(sender), string(meta.BridgeID), string(meta.Room.Receiver), string(meta.ID), string(meta.PartID), typ.Type})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "mooo-history-" + hex.EncodeToString(sum[:]), nil
}

type historyTransport struct{ base http.RoundTripper }

func newHistoryTransport(base http.RoundTripper) *historyTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	if existing, ok := base.(*historyTransport); ok {
		return existing
	}
	return &historyTransport{base: base}
}
func (t *historyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	state, _ := req.Context().Value(historyDeliveryKey).(*historySendState)
	send := strings.LastIndex(req.URL.Path, "/send/")
	if state == nil || req.Method != http.MethodPut || !strings.Contains(req.URL.Path, "/_matrix/client/") || !strings.Contains(req.URL.Path, "/rooms/") || send < 0 {
		return t.base.RoundTrip(req)
	}
	suffix := req.URL.Path[send+len("/send/"):]
	if strings.Count(suffix, "/") != 1 {
		return nil, errors.New("connector: invalid historical Matrix send path")
	}
	clone := req.Clone(req.Context())
	transaction := state.get()
	if strings.SplitN(suffix, "/", 2)[0] != event.EventEncrypted.Type {
		if req.GetBody == nil {
			return nil, errors.New("connector: historical plaintext send body cannot be inspected")
		}
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(body)
		_ = body.Close()
		if err != nil {
			return nil, err
		}
		var payload map[string]json.RawMessage
		if json.Unmarshal(data, &payload) != nil || json.Unmarshal(payload[historyPartMarker], &transaction) != nil || transaction == "" {
			return nil, errors.New("connector: historical plaintext send has no stable part identity")
		}
		delete(payload, historyPartMarker)
		data, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		state.set(transaction)
		_ = req.Body.Close()
		clone.Body = io.NopCloser(bytes.NewReader(data))
		clone.ContentLength = int64(len(data))
		clone.Header.Del("Content-Length")
		clone.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
	}
	if transaction == "" {
		return nil, errors.New("connector: historical Matrix send has no stable transaction identity")
	}
	urlCopy := *req.URL
	clone.URL = &urlCopy
	clone.URL.Path = req.URL.Path[:strings.LastIndex(req.URL.Path, "/")+1] + transaction
	clone.URL.RawPath = ""
	return t.base.RoundTrip(clone)
}
