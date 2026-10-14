package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/frrad/mooo/internal/protocol/macweb"
)

// AcquireHistoryBytes admits one read-only, fully validated dependency. The
// caller's fetch must validate the complete bounded result before returning.
// Physical HTTP steps use HistoryHTTPTransport and a finite 16-exchange budget. Keys are hashed; bodies live only
// in the profile-local concurrent flight, never the scheduling journal.
func (c *Client) AcquireHistoryBytes(ctx context.Context, key string, fetch func(context.Context) ([]byte, error)) ([]byte, error) {
	if c == nil || ctx == nil || key == "" || len(key) > 1<<20 || fetch == nil {
		return nil, ErrProtocol
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClientClosed
	}
	admission := c.historyAdmission
	end := c.beginPersistenceLocked()
	c.mu.Unlock()
	defer end()
	if admission == nil {
		return fetch(ctx)
	}
	return admission.exchange(ctx, "HTTP/"+historyDigest(key), func(ctx context.Context) ([]byte, error) {
		permit := &historyHTTPPermit{admission: admission, active: true}
		result, err := fetch(context.WithValue(ctx, historyHTTPPermitKey{}, permit))
		permit.mu.Lock()
		permit.active = false
		permit.mu.Unlock()
		return result, err
	})
}
func historyReadJSON[T any](c *Client, ctx context.Context, key any, fetch func(context.Context) (T, error)) (T, error) {
	var zero T
	encoded, err := json.Marshal(key)
	if err != nil {
		return zero, ErrProtocol
	}
	data, err := c.AcquireHistoryBytes(ctx, string(encoded), func(ctx context.Context) ([]byte, error) {
		value, err := fetch(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	})
	if err != nil {
		return zero, err
	}
	if json.Unmarshal(data, &zero) != nil {
		return zero, ErrProtocol
	}
	return zero, nil
}

type historyHTTPPermitKey struct{}
type historyHTTPPermit struct {
	admission    *historyAdmission
	mu           sync.Mutex
	requests     int
	lastFinished time.Time
	active       bool
}

// HistoryHTTPTransport counts physical exchanges inside an admitted dependency,
// including redirects. Without an admission context it preserves the base path.
func HistoryHTTPTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if _, ok := base.(historyDependencyTransport); ok {
		return base
	}
	return historyDependencyTransport{base: base}
}

type historyDependencyTransport struct{ base http.RoundTripper }

func (t historyDependencyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	permit, _ := req.Context().Value(historyHTTPPermitKey{}).(*historyHTTPPermit)
	if permit == nil {
		return t.base.RoundTrip(req)
	}
	return permit.roundTrip(req, t.base.RoundTrip)
}

type historyDependencyDoer struct{ base macweb.Doer }

func (d historyDependencyDoer) Do(req *http.Request) (*http.Response, error) {
	if base, ok := d.base.(*http.Client); ok {
		clone := *base
		clone.Transport = HistoryHTTPTransport(base.Transport)
		return clone.Do(req)
	}
	permit, _ := req.Context().Value(historyHTTPPermitKey{}).(*historyHTTPPermit)
	if permit == nil {
		return d.base.Do(req)
	}
	return permit.roundTrip(req, d.base.Do)
}
func (p *historyHTTPPermit) roundTrip(req *http.Request, send func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	p.mu.Lock()
	if !p.active || p.requests >= 16 {
		p.mu.Unlock()
		return nil, ErrHistoryBusy
	}
	if p.requests > 0 {
		wait := time.Until(p.lastFinished.Add(p.admission.policy.MinimumInterval))
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-req.Context().Done():
				timer.Stop()
				p.mu.Unlock()
				return nil, req.Context().Err()
			case <-timer.C:
			}
		}
		p.admission.state.LastErrorAt = time.Now()
		if err := p.admission.persist(p.admission.state.LastErrorAt.Add(p.admission.policy.ErrorBackoff)); err != nil {
			p.mu.Unlock()
			return nil, err
		}
	}
	p.requests++
	response, err := send(req)
	if err != nil {
		p.lastFinished = time.Now()
		p.mu.Unlock()
		return nil, err
	}
	if response == nil || response.Body == nil {
		p.lastFinished = time.Now()
		p.mu.Unlock()
		return nil, ErrProtocol
	}
	response.Body = &historyDependencyBody{ReadCloser: response.Body, finish: func() { p.lastFinished = time.Now(); p.mu.Unlock() }}
	return response, nil
}

type historyDependencyBody struct {
	io.ReadCloser
	once   sync.Once
	finish func()
}

func (b *historyDependencyBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.finish)
	return err
}

// AcquireHistoryResource reuses only fully validated immutable resource results.
// The caller's key must include the attachment/version, not just a message ID.
func (c *Client) AcquireHistoryResource(ctx context.Context, key string, fetch func(context.Context) ([]byte, error)) ([]byte, error) {
	if c == nil || ctx == nil || key == "" || len(key) > 1<<20 || fetch == nil {
		return nil, ErrProtocol
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClientClosed
	}
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	cache := c.historyResources
	end := c.beginPersistenceLocked()
	c.mu.Unlock()
	defer end()
	if cache != nil {
		data, found, err := cache.get(key)
		if err != nil || found {
			return data, err
		}
	}
	return c.AcquireHistoryBytes(ctx, key, func(ctx context.Context) ([]byte, error) {
		if cache != nil {
			data, found, err := cache.get(key)
			if err != nil || found {
				return data, err
			}
		}
		data, err := fetch(ctx)
		if err != nil {
			return nil, err
		}
		if cache != nil {
			if err = cache.put(key, data); err != nil {
				return nil, err
			}
		}
		return data, nil
	})
}
