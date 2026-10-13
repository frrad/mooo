package client

import (
	"context"
	"errors"
	"sort"

	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"go.mongodb.org/mongo-driver/v2/bson"
)

var ErrGapUnresolved = errors.New("client: message gap unresolved")

const maxCatchUpPages = 100

// syncMessages performs one reviewed SYNCMSG page request. Kakao may also
// advance the account's server-visible read watermark through max, so callers
// must treat this as a read-state mutation even though it does not advance the
// local application commit checkpoint.
func (s *session) syncMessages(ctx context.Context, request syncmsg.Request) (syncmsg.Response, error) {
	return call(s, ctx, "SYNCMSG", request, syncmsg.ParseResponse)
}

// syncMessages performs one page through the client's existing session. It
// does not record a read acknowledgement: whether a catch-up page (cnt=0)
// changes the server's read state is unproven, and recording one would make
// MarkRead skip positions the server may still show as unread.
func (c *Client) syncMessages(ctx context.Context, request syncmsg.Request) (syncmsg.Response, error) {
	if c == nil || ctx == nil {
		return syncmsg.Response{}, ErrProtocol
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	return withSession(c, ctx, func(session *session) (syncmsg.Response, error) {
		return session.syncMessages(ctx, request)
	})
}

// InitialSyncTargets returns each synchronized chat's current last-log ceiling.
// Chats without any last log are omitted.
func (c *Client) InitialSyncTargets(ctx context.Context) ([]syncmsg.Target, error) {
	data, err := c.initialChatData(ctx)
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
			return nil, protocolErr("LOGINLIST", "chatDatas")
		}
		if previous, ok := byChat[target.ChatID]; ok && previous != target.MaxLogID {
			return nil, protocolErr("LOGINLIST", "chatDatas.l")
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
// of that commit. A never-committed chat is eligible only after live admission
// durably recorded its first message. Inventory alone does not authorize
// historical backfill. SYNCMSG may affect the selected interval's read state.
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
	state := checkpoint.Snapshot()
	result := make([]syncmsg.Target, 0)
	for _, target := range targets {
		committed := state.CommittedMax(target.ChatID)
		start := state.FirstDeliveryStart(target.ChatID)
		if (committed > 0 && target.MaxLogID > committed) || (committed == 0 && start > 0 && target.MaxLogID >= start) {
			result = append(result, target)
		}
	}
	return result, nil
}

// SeedLastMessage admits the last message that the login inventory already
// carries for a chat this profile has never delivered, so a fresh login can
// show every chat's latest message without requesting history. It returns nil
// without queuing anything when the chat was delivered before, has no last
// message, or accept rejects the decoded event. An admitted event's commit is
// queued as catch-up's are: the caller must deliver and commit it before any
// later event of that chat, or abandon the session.
func (c *Client) SeedLastMessage(chat chatmeta.ChatData, accept func(events.Event) bool) (events.Event, error) {
	if c == nil || chat.ChatID <= 0 || accept == nil {
		return nil, ErrProtocol
	}
	if len(chat.LastChatLog) == 0 {
		return nil, nil
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
	state := checkpoint.Snapshot()
	if state.CommittedMax(chat.ChatID) > 0 || state.FirstDeliveryStart(chat.ChatID) > 0 {
		return nil, nil
	}
	event, err := events.DecodeChatLog(chat.ChatID, chat.LastChatLog)
	if err != nil {
		return nil, err
	}
	chatID, logID, ok := events.MessagePosition(event)
	if !ok || chatID != chat.ChatID {
		return nil, protocolErr("LOGINLIST", "chatDatas.l")
	}
	if !accept(event) {
		return nil, nil
	}
	c.commitMu.Lock()
	defer c.commitMu.Unlock()
	if len(c.pendingCommits[chatID]) > 0 {
		return nil, nil
	}
	if c.pendingCommits == nil {
		c.pendingCommits = make(map[int64][]int64)
	}
	c.pendingCommits[chatID] = []int64{logID}
	return event, nil
}

// CatchUp retrieves the missing interval after this profile's committed chat
// maximum (or recorded first live admission) through targetMax. The result remains uncommitted so a crash before
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
	current := checkpointState.CommittedMax(chatID)
	if current == 0 {
		if start := checkpointState.FirstDeliveryStart(chatID); start > 0 {
			current = start - 1
		}
	}
	if current >= targetMax {
		if checkpointState.HasGapThrough(chatID, targetMax) {
			if err := c.checkpointWrite(func(checkpoint *continuity.Store) error { return checkpoint.ResolveGapThrough(chatID, targetMax) }); err != nil {
				return nil, err
			}
		}
		return []events.Event{}, nil
	}
	gapStart := current + 1

	result := make([]events.Event, 0)
	for range maxCatchUpPages {
		page, err := c.syncMessages(ctx, syncmsg.Request{
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
			if err != nil || logID < current {
				return nil, protocolErr("SYNCMSG", "chatLogs.logId")
			}
			// Membership-only observations show SYNCMSG may include a feed
			// beyond the login ceiling. Do not deliver or commit outside the
			// selected interval; roster reconciliation handles membership.
			if logID > targetMax {
				continue
			}
			// The server may treat cur as inclusive. Repeating exactly the
			// committed boundary is harmless and is not emitted again.
			if logID == current {
				continue
			}
			event, err := events.DecodeChatLog(chatID, raw)
			if err != nil {
				return nil, err
			}
			result = append(result, event)
			current = logID
			progressed = true
		}
		if current == targetMax {
			if checkpointState.HasGapThrough(chatID, targetMax) {
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
