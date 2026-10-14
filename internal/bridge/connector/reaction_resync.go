package connector

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/frrad/mooo/internal/protocol/events"
)

// reactionResyncPages bounds one viewed chat's sync per admitted tick; a longer
// backlog continues from the persisted cursor on a later viewing heartbeat.
const reactionResyncPages = 10

func reactionResyncKey(login string, chatID int64) string {
	return fmt.Sprintf("kakao:reaction-sync:%s:%d", login, chatID)
}

// Viewing clients send a heartbeat every five seconds and no close event.
// Match the Mac's ten-second first tick; a missed heartbeat expires the episode.
const reactionViewingInterval = 10 * time.Second
const reactionViewingExpiry = 15 * time.Second

var errReactionResync = errors.New("connector: reaction metadata sync failed; cursor retained")
var errReactionSyncBusy = errors.New("connector: reaction sync in progress or within its ten-second cooldown")
var errReactionSyncBudget = errors.New("connector: reaction metadata sync page budget exhausted")

type reactionViewingState struct {
	chatID                                  int64
	lastSeen, next                          time.Time
	initialized, disabled, running, pending bool
	latest, synced                          int64
	refreshVersion                          uint64
	cancel                                  context.CancelFunc
}

var _ bridgev2.ChatViewingNetworkAPI = (*KakaoClient)(nil)

// HandleMatrixViewingChat uses explicit viewing heartbeats, never read receipts
// or typing, to recover offline reactions. Standard Matrix appservices do not
// signal viewing; they retain the live-push path without a reconnect sweep.
func (kc *KakaoClient) HandleMatrixViewingChat(ctx context.Context, msg *bridgev2.MatrixViewingChat) error {
	_, err := kc.handleReactionDemand(ctx, msg, false)
	return err
}

// syncRoomReactions is explicit operator demand: it shares the viewing gates
// and budget but need not wait for a first window-timer tick.
func (kc *KakaoClient) syncRoomReactions(ctx context.Context, portal *bridgev2.Portal) (bool, error) {
	return kc.handleReactionDemand(ctx, &bridgev2.MatrixViewingChat{Portal: portal}, true)
}

func (kc *KakaoClient) handleReactionDemand(ctx context.Context, msg *bridgev2.MatrixViewingChat, explicit bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	kc.mu.Lock()
	c := kc.client
	lifecycle := kc.lifecycle
	now := kc.now()
	var chatID int64
	if msg != nil && msg.Portal != nil && msg.Portal.Portal != nil && msg.Portal.Receiver == kc.login.ID && msg.Portal.MXID != "" {
		chatID, _ = parseChatID(msg.Portal.ID)
	}
	if explicit && (chatID <= 0 || c == nil) {
		kc.mu.Unlock()
		if c == nil {
			return false, bridgev2.ErrNotLoggedIn
		}
		return false, errReactionTarget
	}
	if explicit && (kc.reactionSyncBusy || now.Before(kc.reactionSyncNext)) {
		kc.mu.Unlock()
		return false, errReactionSyncBusy
	}
	view := kc.reactionView
	if chatID <= 0 || c == nil {
		if view != nil && view.cancel != nil {
			view.cancel()
		}
		kc.reactionView = nil
		kc.mu.Unlock()
		return false, nil
	}
	if view == nil || view.chatID != chatID || now.Sub(view.lastSeen) >= reactionViewingExpiry {
		if view != nil && view.cancel != nil {
			view.cancel()
		}
		view = &reactionViewingState{chatID: chatID, next: now.Add(reactionViewingInterval), pending: true}
		kc.reactionView = view
	}
	view.lastSeen = now
	if explicit {
		// Operator demand rechecks current room/entry capability, including a
		// room previously disabled during a viewing episode.
		view.initialized, view.disabled = false, false
		view.pending = true
		view.refreshVersion++
		view.next = now
	}
	if kc.reactionSyncBusy || now.Before(kc.reactionSyncNext) || view.running || view.disabled || now.Before(view.next) || (!view.pending && view.latest <= view.synced) {
		kc.mu.Unlock()
		return false, nil
	}
	view.running = true
	kc.reactionSyncBusy = true
	kc.reactionSyncNext = now.Add(reactionViewingInterval)
	view.next = now.Add(reactionViewingInterval)
	initialized := view.initialized
	refreshVersion := view.refreshVersion
	requestCtx, cancel := context.WithTimeout(ctx, reactionViewingExpiry)
	view.cancel = cancel
	kc.mu.Unlock()
	stop := func() bool { return true }
	if lifecycle != nil {
		stop = context.AfterFunc(lifecycle, cancel)
	}
	defer func() { stop(); cancel() }()

	disabled := false
	err := kc.checkSourceAccess(requestCtx, chatID)
	if err == nil && !initialized {
		info, lookupErr := c.ChatInfo(requestCtx, chatID)
		err = lookupErr
		if err == nil && info.ChatData.ChatID != chatID {
			err = errChatInfoMismatch
		}
		if err == nil {
			switch info.ChatData.Type {
			case "DirectChat", "MultiChat", "OD", "OM":
				room, entryErr := c.ChatOnRoom(requestCtx, chatID)
				err = entryErr
				if err == nil && room.ChatID != chatID {
					err = errChatInfoMismatch
				}
				disabled = room.StopSyncLogMeta
			default:
				disabled = true
			}
			initialized = err == nil
		}
	}
	if err == nil && !disabled {
		err = kc.resyncChatReactions(requestCtx, c, msg.Portal.Portal, chatID)
	}
	var synced int64
	if err == nil && !disabled {
		raw, _, readErr := kc.store.get(requestCtx, reactionResyncKey(string(kc.login.ID), chatID))
		err = readErr
		synced, _ = strconv.ParseInt(raw, 10, 64)
	}
	kc.mu.Lock()
	kc.reactionSyncBusy = false
	if kc.reactionView == view {
		view.running, view.cancel = false, nil
		view.initialized, view.disabled = initialized, disabled
		view.pending = err != nil || view.refreshVersion != refreshVersion
		if synced > view.synced {
			view.synced = synced
		}
	}
	kc.mu.Unlock()
	if err != nil {
		if errors.Is(err, errReactionSyncBudget) {
			if explicit {
				return true, errReactionSyncBudget
			}
			return true, nil // Continue the retained demand on a later admitted tick.
		}
		// Do not expose backend error text or source identifiers to the caller.
		kc.log().Warn().Msg("Viewed chat reaction sync stopped; cursor retained")
		return false, errReactionResync
	}
	return !disabled, nil
}

func (kc *KakaoClient) observeReactionRevision(chatID, revision int64) {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	if view := kc.reactionView; view != nil && view.chatID == chatID && revision > view.latest {
		view.latest = revision
	}
}

func (kc *KakaoClient) requestViewedReactionRefresh(chatID int64) {
	kc.mu.Lock()
	defer kc.mu.Unlock()
	if view := kc.reactionView; view != nil && view.chatID == chatID {
		view.refreshVersion++
		view.pending = true
	}
}

// resyncChatReactions is reached only after the viewing/type/chat-entry gates.
// Each page reuses the live revision/delivery path and durable sync cursor.
func (kc *KakaoClient) resyncChatReactions(ctx context.Context, c kakaoClient, portal *database.Portal, chatID int64) error {
	key := reactionResyncKey(string(kc.login.ID), chatID)
	raw, _, readErr := kc.store.get(ctx, key)
	if readErr != nil {
		return readErr
	}
	cur, _ := strconv.ParseInt(raw, 10, 64)
	if cur <= 0 {
		// The official client starts from its oldest stored message.
		first, err := kc.store.firstMessageInPortal(ctx, portal.PortalKey)
		if err != nil {
			return err
		}
		if first == nil {
			return nil
		}
		_, logID, err := parseMessageID(first.ID)
		if err != nil || logID <= 0 {
			return nil
		}
		cur = logID
	}
	for range reactionResyncPages {
		if err := ctx.Err(); err != nil {
			return err
		}
		pageCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		page, err := c.ReactionMetaSync(pageCtx, chatID, cur)
		cancel()
		if err != nil {
			return err
		}
		next := cur
		for _, item := range page.Items {
			evt, err := events.DecodeSyncedLogMeta(item)
			if err != nil {
				return err
			}
			change, ok := evt.(events.ReactionChanged)
			if !ok || change.ChatID != chatID {
				continue
			}
			if change.Revision > next {
				next = change.Revision
			}
			target, err := kc.store.firstMessagePart(ctx, kc.login.ID, makeMessageID(chatID, change.LogID))
			if err != nil {
				return err
			}
			if target == nil {
				// No Matrix event to update for an unbridged message.
				continue
			}
			handled, err := kc.applyReactionChangeContext(ctx, c, change)
			if err != nil || !handled {
				return fmt.Errorf("connector: resynced reaction for log %d was not applied: %v", change.LogID, err)
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if next > cur {
			if err := kc.store.put(ctx, key, strconv.FormatInt(next, 10)); err != nil {
				return err
			}
		}
		if page.Last || next <= cur {
			return nil
		}
		cur = next
	}
	return errReactionSyncBudget
}
