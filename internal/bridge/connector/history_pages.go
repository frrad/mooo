package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

var _ bridgev2.BackfillingNetworkAPI = (*KakaoClient)(nil)
var ErrHistoryBudget = errors.New("connector: history acquisition budget exhausted; acquired progress retained")

type storedHistoryAPI interface {
	ReadStoredHistory(context.Context, client.StoredHistoryRequest) (client.StoredHistoryPage, error)
}
type historyBatch struct {
	Version               int
	Forward               bool
	After, Through, First int64
	Limit                 int
	Pending               bool
	ExcludeThrough        bool
}

func (b historyBatch) valid() bool {
	return b.Version == 1 && b.After >= 0 && b.Through > b.After && b.First > b.After && b.First <= b.Through && b.Limit > 0 && b.Limit <= int(syncmsg.MaxPageSize)
}

func (kc *KakaoClient) historyEnabled() bool {
	if kc.login == nil || kc.login.Bridge == nil {
		return false
	}
	network, ok := kc.login.Bridge.Network.(*KakaoConnector)
	return ok && network.Config.StartupHistory
}

// FetchMessages serves source-ID windows from the acquired store. A per-portal
// pending selection survives partial Matrix sends, independently of framework
// anchors/tasks. Source acquisition has finite budgets and shared pacing.
func (kc *KakaoClient) FetchMessages(ctx context.Context, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	if ctx == nil || !kc.historyEnabled() || params.Portal == nil || params.Portal.Bridge != kc.login.Bridge || params.Portal.Receiver != kc.login.ID || params.ThreadRoot != "" || params.Count < 1 {
		return nil, client.ErrProtocol
	}
	if !params.Forward && !kc.login.Bridge.Matrix.GetCapabilities().BatchSending {
		return nil, errors.New("connector: historical timeline insertion requires batch sending")
	}
	ctx = context.WithValue(ctx, historyResourceOwnerKey{}, kc)
	chatID, err := parseChatID(params.Portal.ID)
	if err != nil {
		return nil, err
	}
	c, err := kc.metadataClient()
	if err != nil {
		return nil, err
	}
	local, ok := c.(storedHistoryAPI)
	if !ok {
		return nil, client.ErrProtocol
	}
	chats, err := c.ListChats(ctx)
	if err != nil {
		return nil, err
	}
	var selected *chatmeta.ChatData
	for i := range chats {
		if chats[i].ChatID == chatID && chats[i].LinkID == 0 && (chats[i].Type == "DirectChat" || chats[i].Type == "MultiChat") {
			selected = &chats[i]
			break
		}
	}
	if selected == nil {
		return nil, errChatInfoMismatch
	}
	if err = kc.checkSourceAccess(ctx, chatID); err != nil {
		return nil, err
	}
	through := selected.LastServerLogID
	if len(selected.LastChatLog) > 0 {
		if last, e := syncmsg.LogID(selected.LastChatLog); e == nil {
			through = max(through, last)
		}
	}
	if through <= 0 {
		return &bridgev2.FetchMessagesResponse{Forward: params.Forward}, nil
	}
	var after int64
	if params.AnchorMessage != nil {
		anchorChat, anchor, parseErr := parseMessageID(params.AnchorMessage.ID)
		if parseErr != nil || anchorChat != chatID || params.AnchorMessage.Room != params.Portal.PortalKey {
			return nil, client.ErrProtocol
		}
		if params.Forward {
			after = anchor
		} else {
			through = anchor
		}
	}
	if params.Forward && params.Cursor != "" {
		return nil, client.ErrProtocol
	}
	if !params.Forward && params.Cursor != "" {
		before, e := parseHistoryCursor(params.Cursor, chatID)
		if e != nil {
			return nil, e
		}
		through = before
	}
	key := fmt.Sprintf("kakao:history-batch:%s:%s:%t", params.Portal.Receiver, params.Portal.ID, params.Forward)
	pending, found, err := kc.loadHistoryBatch(ctx, key)
	if err != nil {
		return nil, err
	}
	limit := min(params.Count, int(syncmsg.MaxPageSize))
	exclusive := !params.Forward && (params.AnchorMessage != nil || params.Cursor != "")
	if found && pending.Pending {
		if pending.Forward != params.Forward {
			return nil, client.ErrProtocol
		}
		after, through, limit = pending.After, pending.Through, pending.Limit
		exclusive = pending.ExcludeThrough
	}
	if through <= after {
		return &bridgev2.FetchMessagesResponse{Forward: params.Forward}, nil
	}
	request := client.StoredHistoryRequest{ChatID: chatID, After: after, Through: through, Limit: limit, ExcludeThrough: exclusive}
	page, err := local.ReadStoredHistory(ctx, request)
	if err != nil {
		return nil, err
	}
	if page.Unavailable != nil {
		return nil, &client.HistoryUnavailableError{Observation: *page.Unavailable}
	}
	if !page.Complete {
		if !params.AllowSlowFetch && !params.Forward {
			return &bridgev2.FetchMessagesResponse{HasMore: true, Cursor: params.Cursor, MoreRequiresSlowFetch: true}, nil
		}
		if params.Forward {
			return nil, ErrHistoryBudget
		} // Startup acquisition must precede delivery.
		acquisitionCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		position := page.CoveredThrough
		if position < after || position > through {
			return nil, client.ErrProtocol
		}
		for pages := 0; pages < 25 && position < through; pages++ {
			acquired, e := c.ReadHistoryPage(acquisitionCtx, chatID, position, through, int(syncmsg.MaxPageSize))
			if e != nil {
				return nil, e
			}
			if acquired.Next <= position {
				return nil, client.ErrGapUnresolved
			}
			position = acquired.Next
		}
		page, err = local.ReadStoredHistory(ctx, request)
		if err != nil {
			return nil, err
		}
		if !page.Complete {
			return nil, ErrHistoryBudget
		}
	}
	result := &bridgev2.FetchMessagesResponse{Forward: params.Forward, HasMore: page.HasMore}
	if len(page.Events) == 0 {
		return result, nil
	}
	_, first, _ := events.MessagePosition(page.Events[0])
	if found && pending.Pending {
		first = pending.First
	} else {
		pending = historyBatch{Version: 1, Forward: params.Forward, After: after, Through: through, First: first, Limit: limit, Pending: true, ExcludeThrough: exclusive}
		if err = kc.saveHistoryBatch(ctx, key, pending); err != nil {
			return nil, err
		}
	}
	result.Cursor = historyCursor(chatID, first)
	var expected []historyExpected
	for _, evt := range page.Events {
		_, position, valid := events.MessagePosition(evt)
		if !valid || position < first {
			continue
		}
		switch evt.(type) {
		case events.MessageEdited, events.MessageDeleted:
			continue
		} // Already applied to target projections.
		remote := kc.remoteEventFor(evt)
		switch evt.(type) {
		case events.MemberAdded, events.MemberRemoved:
			remote = newMessage(kc.messageMeta(chatID, position, 0, 0), makeMessageID(chatID, position), noticeData{Body: "A historical KakaoTalk membership event occurred."}, convertNoticeWithMetadata)
		}
		message, ok := remote.(bridgev2.RemoteMessage)
		if !ok {
			return nil, errors.New("connector: stored event has no historical message mapping")
		}
		intent, ok := params.Portal.GetIntentFor(ctx, message.GetSender(), kc.login, bridgev2.RemoteEventMessage)
		if !ok {
			return nil, bridgev2.ErrFailedToGetIntent
		}
		converted, e := message.ConvertMessage(ctx, params.Portal, intent)
		if e != nil {
			return nil, e
		}
		if converted == nil || len(converted.Parts) == 0 {
			return nil, client.ErrProtocol
		}
		expectedParts := historyExpected{ID: message.GetID()}
		seenParts := make(map[networkid.PartID]bool)
		for _, part := range converted.Parts {
			if part == nil || seenParts[part.ID] {
				return nil, client.ErrProtocol
			}
			seenParts[part.ID] = true
			expectedParts.Parts = append(expectedParts.Parts, part.ID)
		}
		expected = append(expected, expectedParts)
		if !kc.login.Bridge.Matrix.GetCapabilities().BatchSending {
			if e := markMatrixTransactions(params.Portal, intent, message.GetID(), converted, true); e != nil {
				return nil, e
			}
		}
		existing, e := kc.store.messageParts(ctx, params.Portal.Receiver, message.GetID())
		if e != nil {
			return nil, e
		}
		converted.Parts = slices.DeleteFunc(converted.Parts, func(part *bridgev2.ConvertedMessagePart) bool {
			return slices.ContainsFunc(existing, func(row *database.Message) bool { return row.PartID == part.ID })
		})
		if len(converted.Parts) == 0 {
			continue
		}
		timestamp := time.Time{}
		if timed, ok := message.(bridgev2.RemoteEventWithTimestamp); ok {
			timestamp = timed.GetTimestamp()
		}
		result.Messages = append(result.Messages, &bridgev2.BackfillMessage{ConvertedMessage: converted, Sender: message.GetSender(), ID: message.GetID(), Timestamp: timestamp, StreamOrder: position})
	}
	// The framework's cutoff uses timestamp rather than source ordering. Its anchor
	// is a freshly loaded comparison object, never written back to the database.
	// Our source-ID selection and persisted per-part dedup are already authoritative.
	adaptHistoryCutoff(params.AnchorMessage, result.Messages, params.Forward)
	var oldTask database.BackfillTask
	if params.Task != nil {
		oldTask = *params.Task
	}
	result.CompleteCallback = func() {
		confirmed := false
		defer func() {
			if confirmed || params.Task == nil {
				return
			}
			// The pinned executor assigns Cursor/IsDone before sending and adds
			// one batch after this callback. Retain the original cursor and
			// undo that credit when mappings/journal publication aren't confirmed.
			params.Task.Cursor = oldTask.Cursor
			params.Task.IsDone = false
			params.Task.QueueDone = false
			params.Task.BatchCount = oldTask.BatchCount - 1
		}()
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		for _, item := range expected {
			rows, e := kc.store.messageParts(finishCtx, params.Portal.Receiver, item.ID)
			if e != nil {
				return
			}
			for _, part := range item.Parts {
				if !slices.ContainsFunc(rows, func(row *database.Message) bool { return row.PartID == part }) {
					return
				}
			}
		}
		pending.Pending = false
		if e := kc.saveHistoryBatch(finishCtx, key, pending); e != nil {
			kc.log().Error().Err(e).Msg("History delivery selection remains pending")
			return
		}
		confirmed = true
	}
	return result, nil
}

func adaptHistoryCutoff(anchor *database.Message, messages []*bridgev2.BackfillMessage, forward bool) {
	if anchor == nil || len(messages) == 0 {
		return
	}
	// A retry can contain a missing part of the current database anchor.
	// It is already selected/deduplicated by source identity and part ID.
	if slices.ContainsFunc(messages, func(message *bridgev2.BackfillMessage) bool { return message.ID == anchor.ID }) {
		anchor.ID = ""
	}
	boundary := messages[0].Timestamp
	for _, message := range messages {
		if forward && message.Timestamp.Before(boundary) {
			boundary = message.Timestamp
		}
		if !forward && message.Timestamp.After(boundary) {
			boundary = message.Timestamp
		}
	}
	if forward {
		anchor.Timestamp = boundary.Add(-time.Nanosecond)
	} else {
		anchor.Timestamp = boundary.Add(time.Nanosecond)
	}
}
func historyCursor(chat, before int64) networkid.PaginationCursor {
	return networkid.PaginationCursor(fmt.Sprintf("v1:%d:%d", chat, before))
}
func parseHistoryCursor(cursor networkid.PaginationCursor, chatID int64) (int64, error) {
	parts := strings.Split(string(cursor), ":")
	if len(parts) != 3 || parts[0] != "v1" {
		return 0, client.ErrProtocol
	}
	chat, e := strconv.ParseInt(parts[1], 10, 64)
	if e != nil || chat != chatID {
		return 0, client.ErrProtocol
	}
	before, e := strconv.ParseInt(parts[2], 10, 64)
	if e != nil || before <= 0 || historyCursor(chat, before) != cursor {
		return 0, client.ErrProtocol
	}
	return before, nil
}
func (kc *KakaoClient) loadHistoryBatch(ctx context.Context, key string) (historyBatch, bool, error) {
	raw, found, err := kc.store.get(ctx, key)
	if err != nil || !found {
		return historyBatch{}, found, err
	}
	var batch historyBatch
	if len(raw) > 4096 || json.Unmarshal([]byte(raw), &batch) != nil || !batch.valid() {
		return historyBatch{}, false, client.ErrProtocol
	}
	return batch, true, nil
}
func (kc *KakaoClient) saveHistoryBatch(ctx context.Context, key string, batch historyBatch) error {
	if !batch.valid() {
		return client.ErrProtocol
	}
	data, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	return kc.store.put(ctx, key, string(data))
}

type historyExpected struct {
	ID    networkid.MessageID
	Parts []networkid.PartID
}

// Framework forward backfill logs conversion/send errors rather than returning
// them through a chat resync. Do not report a ready connection while its durable
// initial delivery selection is unresolved. Source/live progress stays separate.
func (kc *KakaoClient) checkStartupHistoryDelivery(ctx context.Context, c kakaoClient) error {
	if !kc.historyEnabled() {
		return nil
	}
	chats, err := c.ListChats(ctx)
	if err != nil {
		return err
	}
	for _, chat := range chats {
		key := fmt.Sprintf("kakao:history-batch:%s:%d:true", kc.login.ID, chat.ChatID)
		batch, found, err := kc.loadHistoryBatch(ctx, key)
		if err != nil {
			return err
		}
		if found && batch.Pending {
			return errors.New("connector: initial history delivery remains pending; reconnect to resume")
		}
	}
	return nil
}
