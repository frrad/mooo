package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

type groupHistoryProgress struct {
	After          int64 `json:"after"`
	Through        int64 `json:"through"`
	Remaining      int   `json:"remaining"`
	RequestPending bool  `json:"request_pending"`
	Attempts       int   `json:"attempts"`
	Done           bool  `json:"done"`
}

var commandGroupHistory = &commands.FullHandler{
	Name:                    "history-group",
	Help:                    commands.HelpMeta{Section: commands.HelpSectionChats, Description: "Explicitly import bounded source-visible group history; requests may affect source read state", Args: "<after log ID> <through log ID> <maximum 1–1000> | resume"},
	RequiresLoginPermission: true,
	RequiresEventLevel:      event.StateBridge,
	Func: func(ce *commands.Event) {
		login := ce.User.GetDefaultLogin()
		if login == nil {
			ce.Reply("No Kakao profile is configured")
			return
		}
		kc, ok := login.Client.(*KakaoClient)
		if !ok || login.UserMXID != ce.User.MXID {
			ce.Reply("Select your own Kakao login")
			return
		}
		resume := len(ce.Args) == 1 && ce.Args[0] == "resume"
		var after, through int64
		var budget int
		var err error
		if !resume {
			if len(ce.Args) != 3 {
				ce.Reply("Usage: $cmdprefix history-group <after log ID> <through log ID> <maximum 1–1000> | resume")
				return
			}
			after, err = strconv.ParseInt(ce.Args[0], 10, 64)
			if err == nil {
				through, err = strconv.ParseInt(ce.Args[1], 10, 64)
			}
			if err == nil {
				budget, err = strconv.Atoi(ce.Args[2])
			}
			if err != nil {
				ce.Reply("Invalid history bounds")
				return
			}
		}
		done, err := kc.BackfillGroup(ce.Ctx, ce.RoomID, after, through, budget, resume)
		if err != nil {
			ce.Reply("History import paused or unavailable. Progress was retained; use history-group resume to explicitly retry. Reconnect first if source access or membership is paused. %v", err)
			return
		}
		if done {
			ce.Reply("Selected history interval imported; existing message identities were preserved.")
		} else {
			ce.Reply("History import stopped at its bound or time limit. Progress was retained; resume requires explicit action.")
		}
	},
}

// BackfillGroup is an operator-driven delivery path, not a bootstrap or ticker.
// It uses the ordinary message conversion/dedup path without committing live
// cursors. Replaying historical membership feeds never changes today's roster.
func (kc *KakaoClient) BackfillGroup(ctx context.Context, room id.RoomID, after, through int64, budget int, resume bool) (bool, error) {
	if ctx == nil || kc.login == nil || kc.login.Bridge == nil || !kc.groupGate.TryLock() {
		return false, errors.New("connector: history owner unavailable or busy")
	}
	defer kc.groupGate.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	kc.mu.Lock()
	c := kc.client
	stopping := kc.stopping
	kc.mu.Unlock()
	if c == nil || stopping {
		return false, bridgev2.ErrNotLoggedIn
	}
	p, err := kc.login.Bridge.GetPortalByMXID(ctx, room)
	if err != nil {
		return false, err
	}
	if p == nil || p.Receiver != kc.login.ID {
		return false, errors.New("connector: select an existing group owned by this login")
	}
	chatID, err := parseChatID(p.ID)
	if err != nil {
		return false, err
	}
	if err = kc.checkSourceAccess(ctx, chatID, nil); err != nil {
		return false, err
	}
	key := fmt.Sprintf("kakao:group-history:%s:%s", p.Receiver, p.ID)
	progress, found, err := kc.loadGroupHistory(ctx, key)
	if err != nil {
		return false, err
	}
	if resume {
		if !found {
			return false, errors.New("connector: no selected history interval")
		}
		if progress.Done {
			return true, nil
		}
		if progress.Remaining == 0 {
			return false, errors.New("connector: history message budget exhausted; select a new bounded interval")
		}
	} else {
		if after < 0 || through <= after || budget < 1 || budget > 1000 {
			return false, errors.New("connector: invalid history bounds")
		}
		if found && !progress.Done && progress.Remaining > 0 {
			return false, errors.New("connector: resume the existing history selection before replacing it")
		}
		progress = groupHistoryProgress{After: after, Through: through, Remaining: budget}
	}
	for progress.Remaining > 0 && !progress.Done {
		if err = ctx.Err(); err != nil {
			return false, err
		}
		kc.mu.Lock()
		stopping = kc.stopping || kc.client != c
		kc.mu.Unlock()
		if stopping {
			return false, bridgev2.ErrNotLoggedIn
		}
		// Fresh authoritative membership and regular-room checks precede every page.
		if err = kc.refreshGroupMembership(ctx, c, chatID, false); err != nil {
			return false, err
		}
		info, err := c.ChatInfo(ctx, chatID)
		if err != nil {
			return false, errors.New("connector: history source metadata unavailable")
		}
		if info.ChatData.ChatID != chatID || info.ChatData.Type != "MultiChat" || info.ChatData.LinkID != 0 {
			return false, errors.New("connector: history supports regular groups only")
		}
		ceiling := info.ChatData.LastServerLogID
		if len(info.ChatData.LastChatLog) > 0 {
			if last, e := syncmsg.LogID(info.ChatData.LastChatLog); e == nil && last > ceiling {
				ceiling = last
			}
		}
		if ceiling <= 0 {
			// Read rooms can omit CHATINFO's last-log fields. The original
			// account's login inventory (including observed live maxima) still
			// supplies a bounded ceiling; Matrix mappings are never authority.
			targets, inventoryErr := c.InitialSyncTargets(ctx)
			if inventoryErr != nil {
				return false, errors.New("connector: history source inventory unavailable")
			}
			for _, target := range targets {
				if target.ChatID == chatID && target.MaxLogID > ceiling {
					ceiling = target.MaxLogID
				}
			}
		}
		if ceiling <= 0 || progress.Through > ceiling {
			return false, errors.New("connector: selected history exceeds the source-visible ceiling")
		}
		progress.RequestPending = true
		progress.Attempts++
		if err = kc.saveGroupHistory(ctx, key, progress); err != nil {
			return false, err
		}
		limit := min(progress.Remaining, int(syncmsg.MaxPageSize))
		page, err := c.ReadHistoryPage(ctx, chatID, progress.After, progress.Through, limit)
		if err != nil {
			return false, errors.New("connector: source history unavailable or request outcome unresolved; not retried")
		}
		if len(page.Events) == 0 || len(page.Events) > limit {
			return false, errors.New("connector: history page made no bounded progress")
		}
		// Validate the complete page and its advertised cursor before delivering
		// any content or advancing the durable history position.
		validatedLast := progress.After
		for _, evt := range page.Events {
			roomID, logID, valid := events.MessagePosition(evt)
			if !valid || roomID != chatID || logID <= validatedLast || logID > progress.Through {
				return false, errors.New("connector: history response is outside the selected interval")
			}
			validatedLast = logID
		}
		if page.Next != validatedLast || page.Complete != (validatedLast == progress.Through) {
			return false, errors.New("connector: history response cursor disagrees with its events")
		}
		previous := progress.After
		for _, evt := range page.Events {
			roomID, logID, valid := events.MessagePosition(evt)
			if !valid || roomID != chatID || logID <= previous || logID > progress.Through {
				return false, errors.New("connector: history response is outside the selected interval")
			}
			remote := kc.remoteEventFor(evt)
			switch evt.(type) {
			case events.MemberAdded, events.MemberRemoved:
				remote = newMessage(kc.messageMeta(chatID, logID, 0, 0), makeMessageID(chatID, logID), noticeData{Body: "A historical KakaoTalk membership event occurred."}, convertNoticeWithMetadata)
			}
			if remote == nil {
				return false, errors.New("connector: historical event has no delivery mapping")
			}
			if err = kc.checkSourceAccess(ctx, chatID, nil); err != nil {
				return false, err
			}
			// Sending as a former ghost may temporarily join it via Matrix's
			// intent API. Restore the authoritative current roster before
			// checkpointing that historical delivery or accepting more work.
			restoreRoster := false
			if message, ok := remote.(bridgev2.RemoteMessage); ok && message.GetSender().Sender != "" {
				sender, parseErr := parseUserID(string(message.GetSender().Sender))
				meta, metaOK := p.Metadata.(*KakaoPortalMetadata)
				if parseErr != nil || !metaOK || meta == nil {
					return false, errors.New("connector: historical sender roster is unavailable")
				}
				restoreRoster = !slices.Contains(meta.MembershipRoster, sender)
			}
			historical, ok := remote.(bridgev2.RemoteMessage)
			if !ok {
				return false, errors.New("connector: historical delivery is not a message")
			}
			result := kc.queue(&historyMessage{RemoteMessage: historical, ctx: ctx})
			if restoreRoster {
				if err = kc.refreshGroupMembership(ctx, c, chatID, false); err != nil {
					return false, err
				}
			}
			if !committable(result) || (result.Ignored && !kc.ignoredMessageHasMapping(ctx, historical)) {
				return false, errors.New("connector: history delivery not confirmed")
			}
			previous = logID
			progress.After = logID
			progress.Remaining--
			progress.RequestPending = false
			progress.Done = logID == progress.Through
			if err = kc.saveGroupHistory(ctx, key, progress); err != nil {
				return false, err
			}
		}
		if page.Next != progress.After || page.Complete != progress.Done {
			return false, errors.New("connector: history response cursor disagrees with delivered events")
		}
	}
	return progress.Done, nil
}

func (kc *KakaoClient) loadGroupHistory(ctx context.Context, key string) (groupHistoryProgress, bool, error) {
	raw, found, err := newKVStore(kc.login.Bridge.DB.KV).get(ctx, key)
	if err != nil || !found {
		return groupHistoryProgress{}, false, err
	}
	var p groupHistoryProgress
	if len(raw) > 4096 || json.Unmarshal([]byte(raw), &p) != nil || !p.valid() {
		return p, false, errGroupHistoryInvalid
	}
	return p, true, nil
}

var errGroupHistoryInvalid = errors.New("connector: history journal is invalid")

func (p groupHistoryProgress) valid() bool {
	return p.After >= 0 && p.Through >= p.After && p.Remaining >= 0 && p.Remaining <= 1000 && p.Attempts >= 0 && p.Done == (p.After == p.Through)
}

// saveGroupHistory persists p durably. An out-of-range p is still written, as
// a later load must refuse it, and the save reports the journal invalid.
func (kc *KakaoClient) saveGroupHistory(ctx context.Context, key string, p groupHistoryProgress) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err = newKVStore(kc.login.Bridge.DB.KV).put(ctx, key, string(b)); err != nil {
		return err
	}
	if len(b) > 4096 || !p.valid() {
		return errGroupHistoryInvalid
	}
	return nil
}
