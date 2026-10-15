package client

import (
	"context"
	"slices"
	"time"

	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/bsonfield"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/mchatlogs"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// maxBulkCatchUpPages bounds one connect's MCHATLOGS chain account-wide:
// 8000 logs at the observed 80-log page cap (CT-LIVE-002).
const maxBulkCatchUpPages = maxCatchUpPages

// StartupHistoryOptions bounds the opt-in history acquisition that rides the
// connect's MCHATLOGS chain. Zero values select 25 pages and one minute.
type StartupHistoryOptions struct {
	MaxPages        int
	MaximumDuration time.Duration
}

func (o StartupHistoryOptions) normalized() (StartupHistoryOptions, error) {
	if o.MaxPages == 0 {
		o.MaxPages = 25
	}
	if o.MaximumDuration == 0 {
		o.MaximumDuration = time.Minute
	}
	if o.MaxPages < 1 || o.MaxPages > 1000 || o.MaximumDuration < 0 || o.MaximumDuration > time.Hour {
		return StartupHistoryOptions{}, ErrProtocol
	}
	return o, nil
}

type CatchUpOptions struct {
	// StartupHistory also acquires every regular direct and group room's
	// history into the acquired store on the same chain. It does not advance
	// delivery or read checkpoints, and a room that also needs catch-up is
	// requested from its delivery position.
	StartupHistory *StartupHistoryOptions
}

// CaughtUpRoom is one previously delivered room's missed interval.
type CaughtUpRoom struct {
	ChatID int64
	// Ceiling is the room's login ceiling; nothing past it is delivered.
	Ceiling int64
	// Events are uncommitted. Their commits are queued in order, so the
	// caller must deliver and commit them before any later event of the
	// room, or abandon the session.
	Events []events.Event
	// Skipped reports that the server skipped identities before some of
	// Events. The skipped interval is a recorded gap.
	Skipped bool
	// Unresolved reports that the interval after Events through Ceiling was
	// not recovered. It is a recorded gap.
	Unresolved bool
}

type CatchUpResult struct {
	// Rooms lists every room that needed catch-up, in ascending chat order.
	Rooms []CaughtUpRoom
	Pages int
	Logs  int
	// HistoryPending counts acquisition-only rooms left for a later connect
	// by the startup history budget.
	HistoryPending int
}

type chainRoom struct {
	chatID, ceiling int64
	// cursor is the room's request position: the highest identity returned,
	// including identities past the ceiling.
	cursor int64
	// catchUp rooms deliver; the others only feed the acquired store.
	catchUp  bool
	gapStart int64
	events   []events.Event
	// skipFrom..skipThrough is an interval the server skipped, or empty.
	skipFrom, skipThrough int64
	// done rooms leave the chain before retirement: a discontinuity ends
	// their catch-up.
	done bool
}

func countHistoryRooms(rooms map[int64]*chainRoom, ids []int64) int {
	count := 0
	for _, id := range ids {
		if !rooms[id].catchUp {
			count++
		}
	}
	return count
}

// CatchUpAll recovers, with one MCHATLOGS chain, every message that rooms
// this profile delivered before received while it was away. It follows the
// official client's login catch-up (research/bridge/CONNECT-TRAFFIC.md,
// finding #1): rooms sorted by chat ID, at most 100 per request, the whole
// batch retired on eof and, on a capped page, the rooms below the last log's
// room. Each room's run must continue from its cursor by prevId. A skipped
// predecessor is recorded as a gap and the rest of the run is delivered; any
// other discontinuity, a room retired short of its ceiling, a non-progressing
// page and the page cap record the room's remaining interval as a gap.
// Transport and status failures abort without queuing a commit or recording
// a gap.
//
// MCHATLOGS does not acknowledge reads, unlike SYNCMSG.
func (c *Client) CatchUpAll(ctx context.Context, options CatchUpOptions) (CatchUpResult, error) {
	if c == nil || ctx == nil {
		return CatchUpResult{}, ErrProtocol
	}
	var history *StartupHistoryOptions
	if options.StartupHistory != nil {
		normalized, err := options.StartupHistory.normalized()
		if err != nil {
			return CatchUpResult{}, err
		}
		history = &normalized
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return CatchUpResult{}, ErrClientClosed
	}
	checkpoint, store, admission := c.checkpoint, c.historyStore, c.historyAdmission
	if checkpoint == nil || (history != nil && (store == nil || admission == nil)) {
		c.mu.Unlock()
		return CatchUpResult{}, ErrProtocol
	}
	if admission != nil && c.historyStartWait >= admission.policy.MaxPending {
		c.mu.Unlock()
		return CatchUpResult{}, ErrHistoryBusy
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
	var stopped <-chan struct{}
	if admission != nil {
		stopped = admission.stopCtx.Done()
	}
	// Serialize whole chains: a second invocation must select from the first
	// one's durable cursors.
	select {
	case startup <- struct{}{}:
		defer func() { <-startup }()
	case <-ctx.Done():
		return CatchUpResult{}, ctx.Err()
	case <-stopped:
		return CatchUpResult{}, ErrClientClosed
	}

	rooms, err := c.selectBulkRooms(ctx, checkpoint.Snapshot(), store, history)
	if err != nil {
		return CatchUpResult{}, err
	}
	pending := make([]int64, 0, len(rooms))
	for id, room := range rooms {
		if !room.done {
			pending = append(pending, id)
		}
	}
	slices.Sort(pending)

	result := CatchUpResult{}
	var historyDeadline time.Time
	if history != nil {
		historyDeadline = time.Now().Add(history.MaximumDuration)
	}
	for len(pending) > 0 {
		if history != nil && (result.Pages >= history.MaxPages || !time.Now().Before(historyDeadline)) {
			// The acquisition budget is spent; its rooms keep their durable
			// cursors for a later connect.
			result.HistoryPending += countHistoryRooms(rooms, pending)
			pending = slices.DeleteFunc(pending, func(id int64) bool { return !rooms[id].catchUp })
			history = nil
			continue
		}
		if result.Pages >= maxBulkCatchUpPages {
			break
		}
		ids := pending[:min(len(pending), mchatlogs.MaxChats)]
		request := mchatlogs.Request{ChatIDs: slices.Clone(ids), Sinces: make([]int64, len(ids))}
		for i, id := range ids {
			request.Sinces[i] = rooms[id].cursor
		}
		response, err := c.bulkChatLogs(ctx, admission, request)
		if err != nil {
			return CatchUpResult{}, err
		}
		result.Pages++
		result.Logs += len(response.ChatLogs)
		runs, err := bulkRuns(request, response)
		if err != nil {
			return CatchUpResult{}, err
		}
		progressed := false
		acquired := map[int64]int64{}
		for _, id := range ids {
			room := rooms[id]
			before, wasDone := room.cursor, room.done
			if err := c.applyBulkRun(room, runs[id], store); err != nil {
				return CatchUpResult{}, err
			}
			if room.cursor > before || room.done != wasDone {
				progressed = true
			}
			if room.cursor > before && !room.catchUp {
				acquired[id] = room.cursor
			}
		}
		if len(acquired) > 0 {
			if err := store.commitBulk(acquired); err != nil {
				return CatchUpResult{}, err
			}
		}
		retired := map[int64]bool{}
		if response.EOF {
			for _, id := range ids {
				retired[id] = true
			}
		} else if len(response.ChatLogs) > 0 {
			boundary := bsonfield.Int64OrZero(response.ChatLogs[len(response.ChatLogs)-1], "chatId")
			for _, id := range ids {
				if id < boundary {
					retired[id] = true
				}
			}
		}
		if len(retired) == 0 && !progressed {
			// Neither retirement nor progress: never loop (step 7).
			break
		}
		pending = slices.DeleteFunc(pending, func(id int64) bool { return rooms[id].done || retired[id] })
	}
	result.HistoryPending += countHistoryRooms(rooms, pending)
	if err := c.finishBulkRooms(rooms, checkpoint.Snapshot(), &result); err != nil {
		return CatchUpResult{}, err
	}
	return result, nil
}

// selectBulkRooms returns the catch-up rooms, as ResumeTargets selects them,
// and with startup history the acquisition rooms. A catch-up room whose
// ceiling is below the login floor is unresolved without a request: native
// selection never requests it (CT-LIVE-004).
func (c *Client) selectBulkRooms(ctx context.Context, state continuity.Checkpoint, store *historyStore, history *StartupHistoryOptions) (map[int64]*chainRoom, error) {
	targets, err := c.ResumeTargets(ctx)
	if err != nil {
		return nil, err
	}
	floor, err := withSession(c, ctx, func(s *session) (int64, error) { return s.loginMinLogID, nil })
	if err != nil {
		return nil, err
	}
	rooms := make(map[int64]*chainRoom, len(targets))
	for _, target := range targets {
		cursor := state.CommittedMax(target.ChatID)
		if cursor == 0 {
			cursor = state.FirstDeliveryStart(target.ChatID) - 1
		}
		rooms[target.ChatID] = &chainRoom{
			chatID: target.ChatID, ceiling: target.MaxLogID, cursor: cursor,
			catchUp: true, gapStart: cursor + 1, done: target.MaxLogID < floor,
		}
	}
	if history == nil {
		return rooms, nil
	}
	chats, err := c.ListChats(ctx)
	if err != nil {
		return nil, err
	}
	cursors := store.bulkSnapshot()
	for _, chat := range chats {
		if rooms[chat.ChatID] != nil || chat.LinkID != 0 || (chat.Type != "DirectChat" && chat.Type != "MultiChat") {
			continue
		}
		ceiling := chat.LastServerLogID
		if len(chat.LastChatLog) > 0 {
			if id, err := syncmsg.LogID(chat.LastChatLog); err == nil {
				ceiling = max(ceiling, id)
			}
		}
		if ceiling > 0 && cursors[chat.ChatID] < ceiling && ceiling >= floor {
			rooms[chat.ChatID] = &chainRoom{chatID: chat.ChatID, ceiling: ceiling, cursor: cursors[chat.ChatID]}
		}
	}
	return rooms, nil
}

// bulkChatLogs sends one MCHATLOGS page through the history admission, which
// paces it after any previous history request.
func (c *Client) bulkChatLogs(ctx context.Context, admission *historyAdmission, request mchatlogs.Request) (mchatlogs.Response, error) {
	fetch := func(ctx context.Context) (mchatlogs.Response, error) {
		return withSession(c, ctx, func(s *session) (mchatlogs.Response, error) {
			return call(s, ctx, "MCHATLOGS", request, mchatlogs.ParseResponse)
		})
	}
	if admission == nil {
		return fetch(ctx)
	}
	body, err := request.MarshalBSON()
	if err != nil {
		return mchatlogs.Response{}, err
	}
	payload, err := admission.exchange(ctx, "MCHATLOGS/"+historyDigest(string(body)), func(ctx context.Context) ([]byte, error) {
		response, err := fetch(ctx)
		if err != nil {
			return nil, err
		}
		return bson.Marshal(bson.D{{Key: "eof", Value: response.EOF}, {Key: "chatLogs", Value: response.ChatLogs}})
	})
	if err != nil {
		return mchatlogs.Response{}, err
	}
	return mchatlogs.ParseResponse(payload)
}

// bulkRuns groups a page's logs by room. Every log must belong to a
// requested room and lie at or after its since; ParseResponse already
// checked that rooms are ascending contiguous runs of increasing logIds.
// A log equal to since is dropped, as the server may treat since as
// inclusive.
func bulkRuns(request mchatlogs.Request, response mchatlogs.Response) (map[int64][]bson.Raw, error) {
	since := make(map[int64]int64, len(request.ChatIDs))
	for i, id := range request.ChatIDs {
		since[id] = request.Sinces[i]
	}
	runs := map[int64][]bson.Raw{}
	for _, raw := range response.ChatLogs {
		chatID := bsonfield.Int64OrZero(raw, "chatId")
		cursor, requested := since[chatID]
		logID, err := syncmsg.LogID(raw)
		if !requested || err != nil || logID < cursor {
			return nil, protocolErr("MCHATLOGS", "chatLogs")
		}
		if logID > cursor {
			runs[chatID] = append(runs[chatID], raw)
		}
	}
	return runs, nil
}

// applyBulkRun advances one room by its run on this page.
func (c *Client) applyBulkRun(room *chainRoom, run []bson.Raw, store *historyStore) error {
	if len(run) == 0 {
		return nil
	}
	last, _ := syncmsg.LogID(run[len(run)-1])
	upper := max(room.ceiling, last)
	if !room.catchUp {
		if store != nil {
			if err := store.save(syncmsg.Request{ChatID: room.chatID, Cur: room.cursor, Max: upper}, syncmsg.Response{ChatLogs: run}); err != nil {
				return err
			}
		}
		room.cursor = last
		return nil
	}
	start := room.cursor
	first, ok := syncmsg.PrevID(run[0])
	switch {
	case !ok || first < room.cursor:
		room.done = true
		return nil
	case first > room.cursor:
		// The server skipped (cursor, first]; the native client marks the
		// same missing predecessor. Delivery continues after it.
		if from, through := room.cursor+1, min(first, room.ceiling); from <= through && room.skipFrom == 0 {
			room.skipFrom, room.skipThrough = from, through
		} else if from <= through {
			room.skipThrough = through
		}
		start = first
	}
	previous := start
	decoded := make([]events.Event, 0, len(run))
	for _, raw := range run {
		logID, _ := syncmsg.LogID(raw)
		prev, ok := syncmsg.PrevID(raw)
		if !ok || prev != previous {
			room.done = true
			return nil
		}
		previous = logID
		if logID > room.ceiling {
			// Past the login ceiling: the live stream and the next login
			// cover it. The cursor still moves past it.
			continue
		}
		event, err := events.DecodeChatLog(room.chatID, raw)
		if err != nil {
			return err
		}
		c.gaps.observe("MCHATLOGS", event, raw)
		chatID, position, ok := events.MessagePosition(event)
		if !ok || chatID != room.chatID || position != logID {
			return protocolErr("MCHATLOGS", "chatLogs.chatId")
		}
		decoded = append(decoded, event)
	}
	if store != nil {
		if err := store.save(syncmsg.Request{ChatID: room.chatID, Cur: start, Max: upper}, syncmsg.Response{ChatLogs: run}); err != nil {
			return err
		}
	}
	room.events = append(room.events, decoded...)
	room.cursor = last
	return nil
}

// finishBulkRooms projects each catch-up room's events through the acquired
// store, records its unresolved interval and queues its commits.
func (c *Client) finishBulkRooms(rooms map[int64]*chainRoom, state continuity.Checkpoint, result *CatchUpResult) error {
	ids := make([]int64, 0, len(rooms))
	for id, room := range rooms {
		if room.catchUp {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		room := rooms[id]
		projected, err := c.projectAcquiredEvents(room.events)
		if err != nil {
			projected, room.cursor, room.skipFrom = nil, room.gapStart-1, 0
		}
		// Whatever stopped the room's chain short of its ceiling, the rest
		// of its interval is unrecovered.
		unresolved := room.cursor < room.ceiling
		skipped := room.skipFrom > 0
		if skipped {
			if err := c.checkpointWrite(func(checkpoint *continuity.Store) error {
				return checkpoint.RecordGap(id, room.skipFrom, room.skipThrough)
			}); err != nil {
				return err
			}
		}
		if unresolved {
			from := room.cursor + 1
			if err := c.checkpointWrite(func(checkpoint *continuity.Store) error { return checkpoint.RecordGap(id, from, room.ceiling) }); err != nil {
				return err
			}
		} else if !skipped && state.HasGapThrough(id, room.ceiling) {
			if err := c.checkpointWrite(func(checkpoint *continuity.Store) error { return checkpoint.ResolveGapThrough(id, room.ceiling) }); err != nil {
				return err
			}
		}
		for _, event := range projected {
			chatID, logID, _ := events.MessagePosition(event)
			c.queueCommit(chatID, logID)
		}
		result.Rooms = append(result.Rooms, CaughtUpRoom{ChatID: id, Ceiling: room.ceiling, Events: projected, Skipped: skipped, Unresolved: unresolved})
	}
	return nil
}
