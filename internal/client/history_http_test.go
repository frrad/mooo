package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/reactions"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
)

func TestHistoryHTTPReadCoalescesAndValidatesBeforeCooldownRetirement(t *testing.T) {
	c := accountProfile(t, accountRoot(t), "http", 1000, HistoryPacing{MinimumInterval: time.Millisecond, ErrorBackoff: 100 * time.Millisecond})
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	var calls atomic.Int32
	c.http = historyDependencyDoer{base: friendDoerFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		close(entered)
		<-release
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"1":[7],"revision":4}`))}, nil
	})}
	done := make(chan error, 2)
	read := func() {
		value, err := c.ReactionMembers(t.Context(), 42, 99)
		if err == nil && (value.Revision != 4 || value.Members[reactions.Heart][0] != 7) {
			err = ErrProtocol
		}
		done <- err
	}
	go read()
	<-entered
	go read()
	waitHistoryDemands(t, c.historyAdmission, 2)
	once.Do(func() { close(release) })
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate physical HTTP read", calls.Load())
	}
	// A structurally invalid HTTP 200 must keep the long cooldown too.
	c.http = historyDependencyDoer{base: friendDoerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`not-json`))}, nil
	})}
	if _, err := c.ReactionMembers(t.Context(), 42, 100); err == nil {
		t.Fatal("malformed metadata accepted")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.AcquireHistoryBytes(ctx, "later", func(context.Context) ([]byte, error) { t.Error("validation failure retired backoff"); return nil, nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
func TestHistoryHTTPResourceSharesAccountGateWithLoco(t *testing.T) {
	root := accountRoot(t)
	policy := HistoryPacing{MinimumInterval: time.Millisecond, ErrorBackoff: time.Millisecond}
	a := accountProfile(t, root, "media", 1000, policy)
	b := accountProfile(t, root, "loco", 1000, policy)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	done := make(chan error, 2)
	go func() {
		_, err := a.AcquireHistoryBytes(t.Context(), "resource", func(ctx context.Context) ([]byte, error) {
			close(entered)
			<-release
			return []byte("verified resource"), nil
		})
		done <- err
	}()
	<-entered
	dispatched := make(chan struct{})
	go func() {
		_, err := b.historyAdmission.run(t.Context(), syncmsg.Request{ChatID: 42, Cur: 100, Max: 103}, func(context.Context) (syncmsg.Response, error) {
			close(dispatched)
			return admissionTestResponse(t, 103), nil
		})
		done <- err
	}()
	waitHistoryDemands(t, b.historyAdmission, 1)
	select {
	case <-dispatched:
		t.Fatal("LOCO bypassed media gate")
	default:
	}
	once.Do(func() { close(release) })
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
func TestHistoryHTTPRedirectStepsAreSpacedAndBounded(t *testing.T) {
	c := accountProfile(t, accountRoot(t), "redirect", 1000, HistoryPacing{MinimumInterval: 20 * time.Millisecond, ErrorBackoff: 50 * time.Millisecond})
	var calls int
	var last time.Time
	base := testHTTPTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls > 1 && time.Since(last) < 20*time.Millisecond {
			t.Error("physical redirect bypassed spacing")
		}
		last = time.Now()
		status := 302
		body := ""
		headers := http.Header{"Location": []string{"https://example.invalid/next"}}
		if calls == 3 {
			status = 200
			body = "verified"
			headers = http.Header{}
		}
		return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	transport := HistoryHTTPTransport(HistoryHTTPTransport(base)) // Decoration must be idempotent.
	data, err := c.AcquireHistoryBytes(t.Context(), "redirect", func(ctx context.Context) ([]byte, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", "https://example.invalid/start", nil)
		response, err := (&http.Client{Transport: transport}).Do(req)
		if err != nil {
			return nil, err
		}
		defer func() {
			if err := response.Body.Close(); err != nil {
				t.Error(err)
			}
		}()
		return io.ReadAll(response.Body)
	})
	if err != nil || string(data) != "verified" || calls != 3 {
		t.Fatal(data, calls, err)
	}
	_, err = c.AcquireHistoryBytes(t.Context(), "too-many", func(ctx context.Context) ([]byte, error) {
		for range 17 {
			req, _ := http.NewRequestWithContext(ctx, "GET", "https://example.invalid/resource", nil)
			response, err := transport.RoundTrip(req)
			if err != nil {
				return nil, err
			}
			_ = response.Body.Close()
		}
		return bytes.Repeat([]byte{1}, 1), nil
	})
	if !errors.Is(err, ErrHistoryBusy) {
		t.Fatal("physical request budget not enforced", err)
	}
}

type testHTTPTransport func(*http.Request) (*http.Response, error)

func (t testHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) { return t(req) }

func TestHistoryResourceWarmAndRestartCacheAvoidsSource(t *testing.T) {
	root := accountRoot(t)
	c := accountProfile(t, root, "resource", 1000, HistoryPacing{MinimumInterval: time.Millisecond, ErrorBackoff: time.Millisecond})
	calls := 0
	read := func(context.Context) ([]byte, error) { calls++; return []byte("validated immutable bytes"), nil }
	first, err := c.AcquireHistoryResource(t.Context(), "photo/v1", read)
	if err != nil {
		t.Fatal(err)
	}
	first[0] = 'X' // Callers must not mutate the stored result.
	second, err := c.AcquireHistoryResource(t.Context(), "photo/v1", read)
	if err != nil || string(second) != "validated immutable bytes" || calls != 1 {
		t.Fatal(second, calls, err)
	}
	if err := c.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithOptions(filepath.Join(root, "resource"), nil, OpenOptions{HistoryPacing: HistoryPacing{MinimumInterval: time.Millisecond, ErrorBackoff: time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Shutdown(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	replay, err := reopened.AcquireHistoryResource(t.Context(), "photo/v1", func(context.Context) ([]byte, error) {
		t.Error("restart cache dispatched source")
		return nil, ErrProtocol
	})
	if err != nil || !bytes.Equal(replay, second) {
		t.Fatal(replay, err)
	}
	if _, err = reopened.AcquireHistoryResource(t.Context(), "photo/v2", read); err != nil || calls != 2 {
		t.Fatal("changed resource version reused old data", calls, err)
	}
}
func TestHistoryResourceCorruptionDoesNotRefetchSilently(t *testing.T) {
	c := accountProfile(t, accountRoot(t), "resource", 1000, HistoryPacing{})
	if _, err := c.AcquireHistoryResource(t.Context(), "key", func(context.Context) ([]byte, error) { return []byte("validated"), nil }); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.historyResources.dir, historyDigest("key")+".resource")
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AcquireHistoryResource(t.Context(), "key", func(context.Context) ([]byte, error) {
		t.Error("corruption silently became source miss")
		return nil, nil
	}); !errors.Is(err, errHistoryResourceCache) {
		t.Fatal(err)
	}
}
