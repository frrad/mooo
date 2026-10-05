package client

import (
	"context"
	"errors"
	"sort"

	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"go.mongodb.org/mongo-driver/v2/bson"
)

var ErrGapUnresolved = errors.New("client: message gap unresolved")

const maxCatchUpPages = 100

// SyncMessages performs one reviewed SYNCMSG page request. Kakao may also
// advance the account's server-visible read watermark through max, so callers
// must treat this as a read-state mutation even though it does not advance the
// local application commit checkpoint.
func (s *Session) SyncMessages(ctx context.Context, request syncmsg.Request) (syncmsg.Response, error) {
	if s == nil || ctx == nil {
		return syncmsg.Response{}, ErrProtocol
	}
	body, err := request.MarshalBSON()
	if err != nil {
		return syncmsg.Response{}, err
	}
	reply, err := s.Request(ctx, "SYNCMSG", body)
	if err != nil {
		return syncmsg.Response{}, err
	}
	response, err := syncmsg.ParseResponse(reply.Body)
	if err != nil {
		return syncmsg.Response{}, err
	}
	return response, nil
}

// SyncMessages performs one page through the client's existing session.
func (c *Client) SyncMessages(ctx context.Context, request syncmsg.Request) (syncmsg.Response, error) {
	if c == nil || ctx == nil {
		return syncmsg.Response{}, ErrProtocol
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	session, err := c.ensureSession(ctx)
	if err != nil {
		return syncmsg.Response{}, err
	}
	response, err := session.SyncMessages(ctx, request)
	if err != nil {
		return syncmsg.Response{}, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return syncmsg.Response{}, ErrClientClosed
	}
	checkpoint := c.checkpoint
	endPersistence := c.beginPersistenceLocked()
	c.mu.Unlock()
	defer endPersistence()
	if checkpoint != nil {
		if _, err := checkpoint.CommitReadWatermark(request.ChatID, request.Max); err != nil {
			return syncmsg.Response{}, err
		}
	}
	return response, nil
}

// InitialSyncTargets returns each synchronized chat's current last-log ceiling.
// Chats without any last log are omitted.
func (c *Client) InitialSyncTargets(ctx context.Context) ([]syncmsg.Target, error) {
	data, err := c.InitialChatData(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	checkpoint := c.checkpoint
	c.mu.Unlock()
	if checkpoint != nil {
		known := checkpoint.Snapshot().SyncTargets()
		targets := make([]syncmsg.Target, 0, len(known))
		for _, target := range known {
			if target.MaxLogID > 0 {
				targets = append(targets, syncmsg.Target{ChatID: target.ChatID, MaxLogID: target.MaxLogID})
			}
		}
		return targets, nil
	}
	byChat := make(map[int64]int64)
	for _, raw := range data {
		last, lookupErr := raw.LookupErr("l")
		if lookupErr != nil || last.Type == bson.TypeNull {
			continue
		}
		target, parseErr := syncmsg.TargetFromChatData(raw)
		if parseErr != nil {
			return nil, ErrProtocol
		}
		if previous, ok := byChat[target.ChatID]; ok && previous != target.MaxLogID {
			return nil, ErrProtocol
		}
		byChat[target.ChatID] = target.MaxLogID
	}
	targets := make([]syncmsg.Target, 0, len(byChat))
	for chatID, maxLogID := range byChat {
		targets = append(targets, syncmsg.Target{ChatID: chatID, MaxLogID: maxLogID})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].ChatID < targets[j].ChatID })
	return targets, nil
}

// ResumeTargets returns the chats that need catch-up after a resumed login:
// those this profile has committed before whose server maximum is now ahead
// of that commit. Chats never committed are omitted, because recovering them
// would be an unbounded history backfill rather than resumption, and SYNCMSG
// may mark everything it returns as read.
func (c *Client) ResumeTargets(ctx context.Context) ([]syncmsg.Target, error) {
	targets, err := c.InitialSyncTargets(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	checkpoint := c.checkpoint
	c.mu.Unlock()
	if checkpoint == nil {
		return nil, ErrProtocol
	}
	commits := checkpoint.Snapshot().Chats
	result := make([]syncmsg.Target, 0)
	for _, target := range targets {
		committed := committedMax(commits, target.ChatID)
		if committed > 0 && target.MaxLogID > committed {
			result = append(result, target)
		}
	}
	return result, nil
}

// CatchUp retrieves the missing interval after this profile's committed chat
// maximum through targetMax. The result remains uncommitted so a crash before
// the application persists it causes replay rather than loss. SYNCMSG may make
// that interval appear read to other participants; Kakao's current Mac client
// uses the same operation for explicit read-all. An empty or non-progressing
// server page before targetMax is an explicit gap failure.
func (c *Client) CatchUp(ctx context.Context, chatID, targetMax int64) ([]events.Event, error) {
	if c == nil || ctx == nil || chatID <= 0 || targetMax <= 0 {
		return nil, ErrProtocol
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClientClosed
	}
	checkpoint := c.checkpoint
	c.mu.Unlock()
	if checkpoint == nil {
		return nil, ErrProtocol
	}
	checkpointState := checkpoint.Snapshot()
	current := committedMax(checkpointState.Chats, chatID)
	if current >= targetMax {
		if hasGapThrough(checkpointState.HistoryGaps, chatID, targetMax) {
			if err := c.checkpointWrite(func(checkpoint *continuity.Store) error { return checkpoint.ResolveGapThrough(chatID, targetMax) }); err != nil {
				return nil, err
			}
		}
		return []events.Event{}, nil
	}
	gapStart := current + 1

	result := make([]events.Event, 0)
	for range maxCatchUpPages {
		page, err := c.SyncMessages(ctx, syncmsg.Request{
			// This client holds none of the messages after its commit, so it
			// declares zero and the server returns the missing ones.
			ChatID: chatID, Cur: current, Max: targetMax, Count: 0,
		})
		if err != nil {
			return nil, err
		}
		progressed := false
		for _, raw := range page.ChatLogs {
			logID, err := syncmsg.LogID(raw)
			if err != nil || logID < current || logID > targetMax {
				return nil, ErrProtocol
			}
			// The server may treat cur as inclusive. Repeating exactly the
			// committed boundary is harmless and is not emitted again.
			if logID == current {
				continue
			}
			body, err := bson.Marshal(bson.D{
				{Key: "chatId", Value: chatID},
				{Key: "chatLog", Value: bson.Raw(raw)},
			})
			if err != nil {
				return nil, ErrProtocol
			}
			event, err := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
			if err != nil {
				return nil, err
			}
			result = append(result, event)
			current = logID
			progressed = true
		}
		if current == targetMax {
			if hasGapThrough(checkpointState.HistoryGaps, chatID, targetMax) {
				if err := c.checkpointWrite(func(checkpoint *continuity.Store) error { return checkpoint.ResolveGapThrough(chatID, targetMax) }); err != nil {
					return nil, err
				}
			}
			for _, event := range result {
				chatID, logID, _ := events.MessagePosition(event)
				c.queueCommit(chatID, logID)
			}
			return result, nil
		}
		if !progressed {
			if err := c.checkpointWrite(func(checkpoint *continuity.Store) error { return checkpoint.RecordGap(chatID, gapStart, targetMax) }); err != nil {
				return nil, errors.Join(ErrGapUnresolved, err)
			}
			return nil, ErrGapUnresolved
		}
	}
	if err := c.checkpointWrite(func(checkpoint *continuity.Store) error { return checkpoint.RecordGap(chatID, gapStart, targetMax) }); err != nil {
		return nil, errors.Join(ErrGapUnresolved, err)
	}
	return nil, ErrGapUnresolved
}

func hasGapThrough(gaps []continuity.HistoryGap, chatID, logID int64) bool {
	index := sort.Search(len(gaps), func(i int) bool { return gaps[i].ChatID >= chatID })
	return index < len(gaps) && gaps[index].ChatID == chatID && gaps[index].FromLogID <= logID
}

func committedMax(cursors []continuity.ChatCursor, chatID int64) int64 {
	index := sort.Search(len(cursors), func(i int) bool { return cursors[i].ChatID >= chatID })
	if index < len(cursors) && cursors[index].ChatID == chatID {
		return cursors[index].MaxLogID
	}
	return 0
}
