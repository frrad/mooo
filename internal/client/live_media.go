package client

import (
	"context"
	"sync"
	"time"
)

// Live inbound media has its own lane, separate from the account history
// gate (audit finding 17). The official client downloads a received
// attachment without any traced pacing gate; this lane is bridge policy that
// bounds a photo burst without queueing it behind paced metadata or history
// reads, and without letting a failed CDN transfer extend their error backoff.
const (
	liveMediaConcurrency = 2
	liveMediaSpacing     = 250 * time.Millisecond
)

type liveMediaLane struct {
	once      sync.Once
	slots     chan struct{}
	mu        sync.Mutex
	nextStart time.Time
}

// acquire waits for a transfer slot and the lane's start spacing. The
// returned release frees the slot.
func (l *liveMediaLane) acquire(ctx context.Context) (func(), error) {
	l.once.Do(func() { l.slots = make(chan struct{}, liveMediaConcurrency) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case l.slots <- struct{}{}:
	}
	release := func() { <-l.slots }
	l.mu.Lock()
	start := maxHistoryTime(time.Now(), l.nextStart)
	l.nextStart = start.Add(liveMediaSpacing)
	l.mu.Unlock()
	if wait := time.Until(start); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return release, nil
}

// AcquireLiveMedia downloads one immutable attachment of a live inbound
// message through the live media lane. Like AcquireHistoryResource it reuses
// and stores fully validated results in the profile's resource cache, keyed by
// attachment and version, but it never waits on or advances the account
// history gate.
func (c *Client) AcquireLiveMedia(ctx context.Context, key string, fetch func(context.Context) ([]byte, error)) ([]byte, error) {
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
	release, err := c.liveMedia.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
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
}
