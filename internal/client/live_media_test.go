package client

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A burst of live media runs at most liveMediaConcurrency transfers at once,
// with starts spaced by liveMediaSpacing, independently of the history gate.
func TestLiveMediaLaneBoundsBurstConcurrencyAndSpacing(t *testing.T) {
	c := accountProfile(t, accountRoot(t), "burst", 1000, HistoryPacing{MinimumInterval: time.Hour, ErrorBackoff: time.Hour})
	var active, peak atomic.Int32
	var mu sync.Mutex
	var starts []time.Time
	done := make(chan error, 5)
	for i := range 5 {
		go func() {
			_, err := c.AcquireLiveMedia(t.Context(), fmt.Sprintf("photo/%d", i), func(context.Context) ([]byte, error) {
				now := active.Add(1)
				for {
					old := peak.Load()
					if now <= old || peak.CompareAndSwap(old, now) {
						break
					}
				}
				mu.Lock()
				starts = append(starts, time.Now())
				mu.Unlock()
				time.Sleep(2 * liveMediaSpacing)
				active.Add(-1)
				return []byte("validated photo"), nil
			})
			done <- err
		}()
	}
	for range 5 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if peak.Load() != liveMediaConcurrency {
		t.Fatalf("peak concurrent live transfers: got %d, want %d", peak.Load(), liveMediaConcurrency)
	}
	mu.Lock()
	defer mu.Unlock()
	for i := 1; i < len(starts); i++ {
		// Starts are recorded inside fetch, so allow scheduling jitter.
		if gap := starts[i].Sub(starts[i-1]); gap < liveMediaSpacing-10*time.Millisecond {
			t.Fatalf("live transfer %d started %v after the previous one; spacing is %v", i, gap, liveMediaSpacing)
		}
	}
}

// A live media download neither waits on a paced history read that holds
// the account gate nor, when it fails, extends that gate's error backoff.
func TestLiveMediaLaneIsIndependentOfHistoryGate(t *testing.T) {
	c := accountProfile(t, accountRoot(t), "independent", 1000, HistoryPacing{MinimumInterval: time.Millisecond, ErrorBackoff: time.Hour})
	entered, release := make(chan struct{}), make(chan struct{})
	metadataDone := make(chan error, 1)
	go func() {
		_, err := c.AcquireHistoryBytes(t.Context(), "metadata", func(context.Context) ([]byte, error) {
			close(entered)
			<-release
			return []byte("metadata"), nil
		})
		metadataDone <- err
	}()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	data, err := c.AcquireLiveMedia(ctx, "photo/live", func(context.Context) ([]byte, error) { return []byte("live photo"), nil })
	if err != nil || string(data) != "live photo" {
		t.Fatal("live media waited behind the history gate", data, err)
	}
	transfer := errors.New("synthetic CDN failure")
	if _, err = c.AcquireLiveMedia(ctx, "photo/failed", func(context.Context) ([]byte, error) { return nil, transfer }); !errors.Is(err, transfer) {
		t.Fatal(err)
	}
	close(release)
	if err = <-metadataDone; err != nil {
		t.Fatal(err)
	}
	// The failed live transfer must not impose the hour-long history error
	// backoff on the next metadata read; only MinimumInterval applies.
	metadata, err := c.AcquireHistoryBytes(ctx, "metadata/next", func(context.Context) ([]byte, error) { return []byte("next"), nil })
	if err != nil || string(metadata) != "next" {
		t.Fatal("failed live media extended the history error backoff", err)
	}
	// Validated live results are reused from the resource cache.
	cached, err := c.AcquireLiveMedia(ctx, "photo/live", func(context.Context) ([]byte, error) {
		t.Error("cached live media dispatched source")
		return nil, ErrProtocol
	})
	if err != nil || string(cached) != "live photo" {
		t.Fatal(cached, err)
	}
	// A cached history resource serves a later live delivery too.
	if _, err = c.AcquireHistoryResource(ctx, "photo/history", func(context.Context) ([]byte, error) { return []byte("history photo"), nil }); err != nil {
		t.Fatal(err)
	}
	shared, err := c.AcquireLiveMedia(ctx, "photo/history", func(context.Context) ([]byte, error) {
		t.Error("history-cached resource dispatched source")
		return nil, ErrProtocol
	})
	if err != nil || string(shared) != "history photo" {
		t.Fatal(shared, err)
	}
}
