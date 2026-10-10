package connector

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/reactions"
)

// reactionResyncPages bounds one chat's resync per connection; a longer
// backlog continues from the persisted cursor on the next connection.
const reactionResyncPages = 10

type reactionMetaSyncer interface {
	ReactionMetaSync(ctx context.Context, chatID, cur int64) (reactions.SyncMetaPage, error)
}

func reactionResyncKey(login string, chatID int64) database.Key {
	return database.Key(fmt.Sprintf("kakao:reaction-sync:%s:%d", login, chatID))
}

// resyncReactions recovers reaction changes made while the bridge was away.
// Reactions arrive only as live pushes, so the official client pages the
// server's metadata resync from a per-chat cursor; mooo does the same for
// every bridged chat after catch-up. Each meta goes through the live reaction
// path and its revision guards, so replay is idempotent. A failure leaves the
// chat's cursor for the next connection and never blocks message delivery.
func (kc *KakaoClient) resyncReactions(ctx context.Context, c kakaoClient) {
	syncer, ok := c.(reactionMetaSyncer)
	if !ok || kc.login == nil || kc.login.Bridge == nil || kc.login.Bridge.DB == nil {
		return
	}
	db := kc.login.Bridge.DB
	portals, err := db.Portal.GetAllWithMXID(ctx)
	if err != nil {
		kc.log().Warn().Err(err).Msg("Could not list portals for reaction resync")
		return
	}
	for _, portal := range portals {
		if portal.Receiver != kc.login.ID {
			continue
		}
		chatID, err := parseChatID(portal.ID)
		if err != nil || chatID <= 0 || kc.checkSourceAccess(ctx, chatID, nil) != nil {
			continue
		}
		if err := kc.resyncChatReactions(ctx, c, syncer, db, portal, chatID); err != nil {
			kc.log().Warn().Err(err).Int64("kakao_chat_id", chatID).Msg("Reaction resync stopped; cursor retained")
		}
	}
}

func (kc *KakaoClient) resyncChatReactions(ctx context.Context, c kakaoClient, syncer reactionMetaSyncer, db *database.Database, portal *database.Portal, chatID int64) error {
	key := reactionResyncKey(string(kc.login.ID), chatID)
	cur, _ := strconv.ParseInt(db.KV.Get(ctx, key), 10, 64)
	if cur <= 0 {
		// The official client starts from its oldest stored message.
		first, err := db.Message.GetFirstPortalMessage(ctx, portal.PortalKey)
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
		pageCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		page, err := syncer.ReactionMetaSync(pageCtx, chatID, cur)
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
			target, err := db.Message.GetFirstPartByID(ctx, kc.login.ID, makeMessageID(chatID, change.LogID))
			if err != nil {
				return err
			}
			if target == nil {
				// No Matrix event to update for an unbridged message.
				continue
			}
			handled, err := kc.applyReactionChange(c, change)
			if err != nil || !handled {
				return fmt.Errorf("connector: resynced reaction for log %d was not applied: %v", change.LogID, err)
			}
		}
		if next > cur {
			db.KV.Set(ctx, key, strconv.FormatInt(next, 10))
		}
		if page.Last || next <= cur {
			return nil
		}
		cur = next
	}
	return nil
}
