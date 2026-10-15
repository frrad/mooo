package connector

import (
	"context"
	"errors"
	"time"

	"github.com/frrad/mooo/internal/protocol/events"
)

// Reaction attribution reads, GET .../bubble/reactions/{log}/members and the
// Mini POST /emoticon/chat/rx/log-details, are a deliberate deviation. The
// official client stores only the aggregate and its own pick and sends these
// reads from the reaction detail view; Matrix needs per-actor reactions, so
// the bridge reads them for pushes and viewing resync. These bounds keep the
// deviation small; they are bridge policy, not traced native intervals
// (research/bridge/REACTION-LOOKUP-BUDGET.md).
const (
	// reactionLookupCoalesceWindow: after one read for a message's reactions,
	// further pushes for it within the window share one trailing read.
	reactionLookupCoalesceWindow = 2 * time.Second
	// reactionLookupBurst and reactionLookupRefill are the account's read
	// budget: up to ten reads at once, refilled at one per 20 seconds.
	reactionLookupBurst  = 10
	reactionLookupRefill = 20 * time.Second
	// reactionTrailingTimeout bounds one trailing apply.
	reactionTrailingTimeout = 30 * time.Second
)

// errReactionLookupBudget means the account's attribution-read budget is
// spent. The change is not applied and its revision is not persisted;
// viewing resync recovers it from the durable cursor.
var errReactionLookupBudget = errors.New("connector: Kakao reaction lookup budget exhausted")

// errReactionLookupDeferred means a trailing read for the same message will
// apply the change.
var errReactionLookupDeferred = errors.New("connector: Kakao reaction lookup deferred")

type reactionLookupKey struct {
	chatID, logID int64
	metadataType  int32
}

// reactionLookupWindow is one message's coalescing state: when its last read
// was admitted, and the newest change waiting for the trailing read.
type reactionLookupWindow struct {
	readAt  time.Time
	pending *events.ReactionChanged
	armed   bool
}

// reactionLookupBudget is a token bucket. Its zero value is a full bucket.
type reactionLookupBudget struct {
	started bool
	tokens  float64
	at      time.Time
}

func (b *reactionLookupBudget) take(now time.Time, cost int) bool {
	if !b.started {
		b.started, b.tokens, b.at = true, reactionLookupBurst, now
	}
	if elapsed := now.Sub(b.at); elapsed > 0 {
		b.tokens = min(reactionLookupBurst, b.tokens+float64(elapsed)/float64(reactionLookupRefill))
	}
	b.at = now
	if b.tokens < float64(cost) {
		return false
	}
	b.tokens -= float64(cost)
	return true
}

// takeReactionLookups charges the account budget for one change's reads.
func (kc *KakaoClient) takeReactionLookups(change events.ReactionChanged) bool {
	cost := 1
	if change.MetadataType == 2 {
		cost = 2 // members and Mini log details
	}
	kc.reactionLookupMu.Lock()
	defer kc.reactionLookupMu.Unlock()
	return kc.reactionBudget.take(kc.now(), cost)
}

// coalesceReactionLookup admits a live push's read, or folds the push into
// the message's trailing read when one was admitted within the window. It
// reports whether the push was deferred.
func (kc *KakaoClient) coalesceReactionLookup(change events.ReactionChanged) bool {
	key := reactionLookupKey{change.ChatID, change.LogID, change.MetadataType}
	kc.reactionLookupMu.Lock()
	now := kc.now()
	for k, w := range kc.reactionLookups {
		if w.pending == nil && !w.armed && now.Sub(w.readAt) >= reactionLookupCoalesceWindow {
			delete(kc.reactionLookups, k)
		}
	}
	w := kc.reactionLookups[key]
	if w == nil || (w.pending == nil && now.Sub(w.readAt) >= reactionLookupCoalesceWindow) {
		if kc.reactionLookups == nil {
			kc.reactionLookups = make(map[reactionLookupKey]*reactionLookupWindow)
		}
		kc.reactionLookups[key] = &reactionLookupWindow{readAt: now}
		kc.reactionLookupMu.Unlock()
		return false
	}
	if w.pending == nil || change.Revision > w.pending.Revision {
		pending := change
		w.pending = &pending
	}
	arm := !w.armed
	w.armed = true
	delay := max(reactionLookupCoalesceWindow-now.Sub(w.readAt), time.Millisecond)
	kc.reactionLookupMu.Unlock()
	if arm {
		kc.afterFunc(delay, func() { kc.flushReactionLookup(key) })
	}
	return true
}

// flushReactionLookup applies a message's newest deferred change with one read
// on the current session. A dropped session leaves it to viewing resync.
func (kc *KakaoClient) flushReactionLookup(key reactionLookupKey) {
	kc.reactionLookupMu.Lock()
	w := kc.reactionLookups[key]
	if w == nil || w.pending == nil {
		if w != nil {
			w.armed = false
		}
		kc.reactionLookupMu.Unlock()
		return
	}
	change := *w.pending
	w.pending, w.armed, w.readAt = nil, false, kc.now()
	kc.reactionLookupMu.Unlock()

	kc.mu.Lock()
	c := kc.client
	lifecycle := kc.lifecycle
	kc.mu.Unlock()
	if c == nil {
		kc.log().Debug().Msg("Dropping deferred Kakao reaction read: not connected")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), reactionTrailingTimeout)
	defer cancel()
	if lifecycle != nil {
		stop := context.AfterFunc(lifecycle, cancel)
		defer stop()
	}
	if _, err := kc.applyReaction(ctx, c, change, false); err != nil {
		kc.reactionPushFailure(change, err)
	}
}

// reactionPushFailure handles a live push that could not be applied. A spent
// budget is not a failure: the viewed room, if this is it, resyncs.
func (kc *KakaoClient) reactionPushFailure(change events.ReactionChanged, err error) bool {
	if errors.Is(err, errReactionLookupBudget) {
		kc.log().Debug().Msg("Kakao reaction lookup budget spent; leaving the change for viewing resync")
		kc.requestViewedReactionRefresh(change.ChatID)
		return true
	}
	return kc.reportReactionFailure(change, err)
}
