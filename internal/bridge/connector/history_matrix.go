package connector

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type historyContextKey uint8

const (
	historyDeliveryKey historyContextKey = iota
	historyTransactionKey
)

// historyMessage preserves normal conversion and identity, but bounds handling
// by the operator call and marks only historical message sends as idempotent.
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
	return context.WithValue(bounded, historyDeliveryKey, true)
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

// The SDK handles encryption, double puppeting, joining and message profiles.
// This adapter only supplies a stable transaction identity at the HTTP boundary.
type historyMatrixConnector struct{ *matrix.Connector }

func (c *historyMatrixConnector) GhostIntent(user networkid.UserID) bridgev2.MatrixAPI {
	return wrapHistoryIntent(c.Connector.GhostIntent(user))
}
func (c *historyMatrixConnector) BotIntent() bridgev2.MatrixAPI {
	return wrapHistoryIntent(c.Connector.BotIntent())
}
func (c *historyMatrixConnector) NewUserIntent(ctx context.Context, user id.UserID, token string) (bridgev2.MatrixAPI, string, error) {
	intent, newToken, err := c.Connector.NewUserIntent(ctx, user, token)
	if err != nil || intent == nil {
		return intent, newToken, err
	}
	as := intent.(*matrix.ASIntent)
	// A newly configured double puppet has its own HTTP client. Clone it instead
	// of modifying a shared default transport.
	if _, ok := as.Matrix.Client.Client.Transport.(*historyTransport); !ok {
		cloned := *as.Matrix.Client.Client
		cloned.Transport = newHistoryTransport(cloned.Transport)
		as.Matrix.Client.Client = &cloned
	}
	return wrapHistoryIntent(intent), newToken, nil
}
func installHistoryMatrix(br *bridgev2.Bridge) {
	base, ok := br.Matrix.(*matrix.Connector)
	if !ok {
		return
	}
	base.AS.HTTPClient.Transport = newHistoryTransport(base.AS.HTTPClient.Transport)
	wrapped := &historyMatrixConnector{Connector: base}
	br.Matrix = wrapped
	br.Bot = wrapped.BotIntent()
}
func wrapHistoryIntent(intent bridgev2.MatrixAPI) bridgev2.MatrixAPI {
	return &historyIntent{ASIntent: intent.(*matrix.ASIntent)}
}

type historyIntent struct{ *matrix.ASIntent }

func (i *historyIntent) SendMessage(ctx context.Context, room id.RoomID, typ event.Type, content *event.Content, extra *bridgev2.MatrixSendExtra) (*mautrix.RespSendEvent, error) {
	if marked, _ := ctx.Value(historyDeliveryKey).(bool); marked {
		if extra == nil || extra.MessageMeta == nil || extra.MessageMeta.ID == "" {
			return nil, errors.New("connector: historical send has no stable message identity")
		}
		transaction, err := historyTransactionID(room, i.GetMXID(), extra.MessageMeta, typ)
		if err != nil {
			return nil, err
		}
		ctx = context.WithValue(ctx, historyTransactionKey, transaction)
	}
	return i.ASIntent.SendMessage(ctx, room, typ, content, extra)
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
	transaction, _ := req.Context().Value(historyTransactionKey).(string)
	if transaction != "" {
		marker := "/_matrix/client/"
		send := strings.LastIndex(req.URL.Path, "/send/")
		if req.Method != http.MethodPut || !strings.Contains(req.URL.Path, marker) || !strings.Contains(req.URL.Path, "/rooms/") || send < 0 {
			// Key sharing, membership and profile lookups retain SDK behavior.
			return t.base.RoundTrip(req)
		}
		suffix := req.URL.Path[send+len("/send/"):]
		if strings.Count(suffix, "/") != 1 {
			return nil, errors.New("connector: invalid historical Matrix send path")
		}
		clone := req.Clone(req.Context())
		urlCopy := *req.URL
		clone.URL = &urlCopy
		clone.URL.Path = req.URL.Path[:strings.LastIndex(req.URL.Path, "/")+1] + transaction
		clone.URL.RawPath = ""
		return t.base.RoundTrip(clone)
	}
	return t.base.RoundTrip(req)
}
