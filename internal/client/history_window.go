package client

import (
	"context"
	"path/filepath"
	"slices"

	"github.com/frrad/mooo/internal/privatejson"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// StoredHistoryRequest selects a local chronological window. Through is an
// observed source identity, not an ID-minus-page-size calculation. ExcludeThrough
// makes a backward anchor exclusive without inventing its predecessor.
type StoredHistoryRequest struct {
	ChatID, After, Through int64
	Limit                  int
	ExcludeThrough         bool
}

type StoredHistoryPage struct {
	Events   []events.Event
	HasMore  bool // More acquired rows precede the returned window.
	Complete bool // The selected source interval is covered by connected acquired pages.
}

// ReadStoredHistory returns the newest local rows in ascending identity order.
// It never connects or acquires history, advances delivery, or acknowledges reads.
// Complete describes acquired interval coverage, not server retention/UI parity.
func (c *Client) ReadStoredHistory(ctx context.Context, request StoredHistoryRequest) (StoredHistoryPage, error) {
	if c == nil || ctx == nil || request.Limit < 1 || request.Limit > int(syncmsg.MaxPageSize) {
		return StoredHistoryPage{}, ErrProtocol
	}
	req := syncmsg.Request{ChatID: request.ChatID, Cur: request.After, Max: request.Through}
	if _, err := req.MarshalBSON(); err != nil {
		return StoredHistoryPage{}, err
	}
	if err := ctx.Err(); err != nil {
		return StoredHistoryPage{}, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return StoredHistoryPage{}, ErrClientClosed
	}
	store := c.historyStore
	if store == nil {
		c.mu.Unlock()
		return StoredHistoryPage{}, ErrProtocol
	}
	end := c.beginPersistenceLocked()
	c.mu.Unlock()
	defer end()
	return store.window(ctx, request)
}

func (s *historyStore) window(ctx context.Context, request StoredHistoryRequest) (StoredHistoryPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return StoredHistoryPage{}, s.failed
	}
	covered := request.After
	for {
		next := covered
		for _, ref := range s.pages {
			if ref.request.ChatID == request.ChatID && ref.request.Cur <= covered && ref.last > covered {
				next = max(next, min(ref.last, request.Through))
			}
		}
		if next == covered {
			break
		}
		covered = next
	}
	rows := map[int64]bson.Raw{}
	for _, ref := range s.pages {
		if ref.request.ChatID != request.ChatID || ref.last <= request.After || ref.request.Cur >= request.Through {
			continue
		}
		if err := ctx.Err(); err != nil {
			return StoredHistoryPage{}, err
		}
		page, found, err := privatejson.Read[acquiredPage](filepath.Join(s.dir, ref.name))
		if err != nil || !found || page.Version != 1 || page.Binding != s.binding || historyPageName(page.Request) != ref.name {
			s.failed = ErrHistoryStoreCorrupt
			return StoredHistoryPage{}, s.failed
		}
		if _, err = validateAcquiredPage(page.Request, page.Response); err != nil {
			s.failed = ErrHistoryStoreCorrupt
			return StoredHistoryPage{}, s.failed
		}
		for _, raw := range page.Response.ChatLogs {
			id, _ := syncmsg.LogID(raw)
			if id > request.After && id <= request.Through && (!request.ExcludeThrough || id != request.Through) {
				rows[id] = raw
			}
		}
	}
	ids := make([]int64, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := StoredHistoryPage{Complete: covered >= request.Through, HasMore: len(ids) > request.Limit}
	ids = ids[max(0, len(ids)-request.Limit):]
	for _, id := range ids {
		raw, err := s.apply(rows[id], request.ChatID)
		if err != nil {
			return StoredHistoryPage{}, err
		}
		evt, err := events.DecodeChatLog(request.ChatID, raw)
		if err != nil {
			return StoredHistoryPage{}, err
		}
		out.Events = append(out.Events, evt)
	}
	return out, nil
}
