package client

import (
	"context"
	"slices"
	"time"

	"github.com/frrad/mooo/internal/protocol/bsonfield"
	"github.com/frrad/mooo/internal/protocol/mchatlogs"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type StartupHistoryOptions struct {
	// ChatIDs optionally bounds a research/operator selection. Empty selects all
	// regular direct/group rooms in the complete inventory, independently of unread.
	ChatIDs         []int64
	MaxPages        int
	MaximumDuration time.Duration
}
type StartupHistoryResult struct{ Pages, Logs, Pending int }

// AcquireStartupHistory executes the ordinary bulk chain into the acquired
// store. It does not advance delivery/read checkpoints or import cloud backup.
// Budget exhaustion leaves persisted cursors for a later explicit invocation.
func (c *Client) AcquireStartupHistory(ctx context.Context, options StartupHistoryOptions) (StartupHistoryResult, error) {
	if c == nil || ctx == nil {
		return StartupHistoryResult{}, ErrProtocol
	}
	if options.MaxPages == 0 {
		options.MaxPages = 25
	}
	if options.MaximumDuration == 0 {
		options.MaximumDuration = time.Minute
	}
	if options.MaxPages < 1 || options.MaxPages > 1000 || options.MaximumDuration < 0 || options.MaximumDuration > time.Hour {
		return StartupHistoryResult{}, ErrProtocol
	}
	ctx, cancel := context.WithTimeout(ctx, options.MaximumDuration)
	defer cancel()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return StartupHistoryResult{}, ErrClientClosed
	}
	store, admission := c.historyStore, c.historyAdmission
	if store == nil || admission == nil {
		c.mu.Unlock()
		return StartupHistoryResult{}, ErrProtocol
	}
	if c.historyStartWait >= admission.policy.MaxPending {
		c.mu.Unlock()
		return StartupHistoryResult{}, ErrHistoryBusy
	}
	c.historyStartWait++
	end := c.beginPersistenceLocked()
	if c.historyStartup == nil {
		c.historyStartup = make(chan struct{}, 1)
	}
	startup := c.historyStartup
	c.mu.Unlock()
	defer end()
	defer func() {
		c.mu.Lock()
		c.historyStartWait--
		c.mu.Unlock()
	}()
	// Serialize whole coordinators, not only transport calls: a second invocation
	// must rebuild its eligibility from the first one's durable acquired cursor.
	select {
	case startup <- struct{}{}:
		defer func() { <-startup }()
	case <-ctx.Done():
		return StartupHistoryResult{}, ctx.Err()
	case <-admission.stopCtx.Done():
		return StartupHistoryResult{}, ErrClientClosed
	}
	chats, err := c.ListChats(ctx)
	if err != nil {
		return StartupHistoryResult{}, err
	}
	floor, err := withSession(c, ctx, func(s *session) (int64, error) { return s.loginMinLogID, nil })
	if err != nil {
		return StartupHistoryResult{}, err
	}
	ceilings := map[int64]int64{}
	wanted := map[int64]bool{}
	found := map[int64]bool{}
	for _, id := range options.ChatIDs {
		if id <= 0 || wanted[id] {
			return StartupHistoryResult{}, ErrProtocol
		}
		wanted[id] = true
	}
	for _, chat := range chats {
		if len(wanted) > 0 && !wanted[chat.ChatID] {
			continue
		}
		if chat.LinkID != 0 || (chat.Type != "DirectChat" && chat.Type != "MultiChat") {
			continue
		}
		found[chat.ChatID] = true
		ceiling := chat.LastServerLogID
		if len(chat.LastChatLog) > 0 {
			if id, e := syncmsg.LogID(chat.LastChatLog); e == nil {
				ceiling = max(ceiling, id)
			}
		}
		if ceiling > 0 {
			ceilings[chat.ChatID] = ceiling
		}
	}
	for id := range wanted {
		if !found[id] {
			return StartupHistoryResult{}, ErrProtocol
		}
	}
	cursors := store.bulkSnapshot()
	pending := make([]int64, 0, len(ceilings))
	for id, last := range ceilings {
		if cursors[id] < last && last >= floor {
			pending = append(pending, id)
		}
	}
	slices.Sort(pending)
	result := StartupHistoryResult{Pending: len(pending)}
	for len(pending) > 0 && result.Pages < options.MaxPages {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		ids := append([]int64(nil), pending[:min(len(pending), mchatlogs.MaxChats)]...)
		request := mchatlogs.Request{ChatIDs: ids, Sinces: make([]int64, len(ids))}
		for i, id := range ids {
			request.Sinces[i] = cursors[id]
		}
		body, err := request.MarshalBSON()
		if err != nil {
			return result, err
		}
		var retired []int64
		payload, err := admission.exchange(ctx, "MCHATLOGS/"+historyDigest(string(body)), func(ctx context.Context) ([]byte, error) {
			response, err := withSession(c, ctx, func(s *session) (mchatlogs.Response, error) {
				return call(s, ctx, "MCHATLOGS", request, mchatlogs.ParseResponse)
			})
			if err != nil {
				return nil, err
			}
			selected := map[int64]bool{}
			for _, id := range ids {
				selected[id] = true
			}
			grouped := map[int64][]bson.Raw{}
			next := map[int64]int64{}
			for _, raw := range response.ChatLogs {
				id := bsonfield.Int64OrZero(raw, "chatId")
				position, e := syncmsg.LogID(raw)
				if e != nil || !selected[id] || position < cursors[id] {
					return nil, mchatlogs.ErrInvalid
				}
				if position > cursors[id] {
					grouped[id] = append(grouped[id], raw)
					next[id] = position
				}
			}
			if response.EOF {
				retired = ids
			} else if len(response.ChatLogs) > 0 {
				boundary := bsonfield.Int64OrZero(response.ChatLogs[len(response.ChatLogs)-1], "chatId")
				for _, id := range ids {
					if id < boundary {
						retired = append(retired, id)
					}
				}
			}
			if len(retired) == 0 && len(next) == 0 {
				return nil, ErrGapUnresolved
			}
			// Persist all complete room projections before advancing any bulk cursor.
			for _, id := range ids {
				logs := grouped[id]
				if len(logs) == 0 {
					continue
				}
				upper := max(ceilings[id], next[id])
				if err := store.save(syncmsg.Request{ChatID: id, Cur: cursors[id], Max: upper}, syncmsg.Response{ChatLogs: logs}); err != nil {
					return nil, err
				}
			}
			if err := store.commitBulk(next); err != nil {
				return nil, err
			}
			return bson.Marshal(bson.D{{Key: "eof", Value: response.EOF}, {Key: "chatLogs", Value: response.ChatLogs}})
		})
		if err != nil {
			return result, err
		}
		// Reconstruct retirement for a coalesced caller too; closure state belongs to
		// the owner, whereas the persisted response/cursors belong to every caller.
		response, err := mchatlogs.ParseResponse(payload)
		if err != nil {
			return result, err
		}
		retired = retired[:0]
		if response.EOF {
			retired = ids
		} else if len(response.ChatLogs) > 0 {
			boundary := bsonfield.Int64OrZero(response.ChatLogs[len(response.ChatLogs)-1], "chatId")
			for _, id := range ids {
				if id < boundary {
					retired = append(retired, id)
				}
			}
		}
		cursors = store.bulkSnapshot()
		result.Pages++
		result.Logs += len(response.ChatLogs)
		pending = slices.DeleteFunc(pending, func(id int64) bool { return slices.Contains(retired, id) })
		result.Pending = len(pending)
	}
	return result, nil
}
