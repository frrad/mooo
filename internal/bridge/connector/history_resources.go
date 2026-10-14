package connector

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/frrad/mooo/internal/client"
)

type historyResourceOwnerKey struct{}
type historyResourceAPI interface {
	AcquireHistoryResource(context.Context, string, func(context.Context) ([]byte, error)) ([]byte, error)
}

func historyResourceBytes(ctx context.Context, key any, fetch func(context.Context) ([]byte, error)) ([]byte, error) {
	return historyDependencyBytes(ctx, key, fetch, true)
}

func historyDependencyBytes(ctx context.Context, key any, fetch func(context.Context) ([]byte, error), immutable bool) ([]byte, error) {
	owner, _ := ctx.Value(historyResourceOwnerKey{}).(*KakaoClient)
	if owner == nil || owner.login == nil || owner.login.Bridge == nil {
		return fetch(ctx)
	}
	c, err := owner.metadataClient()
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(key)
	if err != nil {
		return nil, client.ErrProtocol
	}
	if immutable {
		admission, ok := c.(historyResourceAPI)
		if !ok {
			return nil, client.ErrProtocol
		}
		return admission.AcquireHistoryResource(ctx, string(encoded), fetch)
	}
	admission, ok := c.(interface {
		AcquireHistoryBytes(context.Context, string, func(context.Context) ([]byte, error)) ([]byte, error)
	})
	if !ok {
		return nil, client.ErrProtocol
	}
	return admission.AcquireHistoryBytes(ctx, string(encoded), fetch)
}
func historyResourceJSON[T any](ctx context.Context, key any, fetch func(context.Context) (T, error)) (T, error) {
	var out T
	data, err := historyResourceBytes(ctx, key, func(ctx context.Context) ([]byte, error) {
		value, err := fetch(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	})
	if err != nil {
		return out, err
	}
	if json.Unmarshal(data, &out) != nil {
		return out, client.ErrProtocol
	}
	return out, nil
}

func historyHTTPClient(base *http.Client) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	clone := *base
	clone.Transport = client.HistoryHTTPTransport(base.Transport)
	return &clone
}
