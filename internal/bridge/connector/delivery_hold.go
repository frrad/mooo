package connector

import (
	"context"
	"time"

	"maunium.net/go/mautrix/bridgev2/status"

	"github.com/frrad/mooo/internal/protocol/events"
)

// Delivery holds keep a source session alive when one chat's event cannot be
// delivered yet.
//
// An event fails delivery when a dependency fails: Matrix, an inbound media
// download, the GETMSGS read that completes an edit, or the roster read of a
// membership notice. None of these is a failed Kakao session, and no traced
// official-client path turns one into logout or recovery
// (research/bridge/NATIVE-SKEW-AUDIT.md finding 2). Ending the session would
// replay the same event through catch-up after a full relogin, so a
// persistent failure would repeat the login on the reconnect schedule.
//
// Instead the event pump holds the failed chat. The failed event and the
// chat's later events stay uncommitted and are buffered in order, while other
// chats keep flowing; the client commits per chat, so this never moves a
// chat's cursor past an undelivered event. A genuine transport failure still
// ends the session, because the source stream closes.
//
// A held chat is retried after heldChatRetryDelays. When those attempts are
// exhausted the chat is paused and the bridge reports
// stateChatDeliveryPaused; it is not retried on a timer again. A chat whose
// source access is paused (unconverged membership or removal) is not retried
// on a timer at all, because its retry cannot succeed before that state
// changes. A paused chat resumes when its membership converges, when the
// operator sends the reconnect command, or at the next session, whose
// catch-up replays the uncommitted interval.
//
// Events that belong to no chat, such as terminal notices, cannot be held and
// still end the session when they fail.

// maxHeldEvents bounds one held chat's buffer. Later positioned events are
// dropped uncommitted and read again by catch-up when the chat resumes.
const maxHeldEvents = 1024

// heldChatRetryDelays are the in-session retries of a held chat. They are
// the bridge's own policy; no native path retries a failed delivery.
var heldChatRetryDelays = []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 60 * time.Second}

const stateChatDeliveryPaused status.BridgeStateErrorCode = "kakao-chat-delivery-paused"

func init() {
	status.BridgeStateHumanErrors.Update(status.BridgeStateErrorMap{
		stateChatDeliveryPaused: "Delivery from a Kakao chat is paused after repeated failures. Its progress was retained. Send the reconnect command to retry it; it is also replayed on the next connection.",
	})
}

// heldChat is one chat whose delivery is waiting for a retry.
type heldChat struct {
	events []events.Event
	// attempts counts the failed retries since the chat was held or resumed.
	attempts int
	// due is when the next timed retry runs; zero waits for a resume trigger.
	due    time.Time
	paused bool
	// missedThrough is the highest positioned log dropped after the buffer
	// filled. Catch-up reads it again before the chat resumes live delivery.
	missedThrough int64
}

// deliveryHolds is owned by the session's bootstrap and then by its event
// pump; they never run concurrently.
type deliveryHolds struct {
	chats map[int64]*heldChat
}

func newDeliveryHolds() *deliveryHolds {
	return &deliveryHolds{chats: map[int64]*heldChat{}}
}

// heldChatOf names the chat whose delivery order an event belongs to.
func heldChatOf(evt events.Event) (int64, bool) {
	if chatID, _, ok := events.MessagePosition(evt); ok {
		return chatID, true
	}
	switch evt := evt.(type) {
	case events.ReactionChanged:
		return evt.ChatID, true
	case events.ReadStateChanged:
		return evt.ChatID, true
	case events.ChatLeft:
		return evt.ChatID, true
	case events.ChatStatusChanged:
		return evt.ChatID, true
	case events.ChatMetaChanged:
		return evt.ChatID, true
	case events.ChatMoimMetaChanged:
		return evt.ChatID, true
	case events.UnsupportedLogMeta:
		return evt.ChatID, true
	}
	return 0, false
}

// deliver handles one source event in order. It reports false only for a
// failed event that belongs to no chat, which must end the session.
func (kc *KakaoClient) deliver(c kakaoClient, evt events.Event) bool {
	chatID, scoped := heldChatOf(evt)
	if scoped {
		if held := kc.holds.chats[chatID]; held != nil {
			held.add(evt)
			return true
		}
	}
	if kc.handleEvent(c, evt) {
		return true
	}
	if !scoped {
		return false
	}
	kc.hold(chatID, []events.Event{evt})
	return true
}

func (h *heldChat) add(evt events.Event) {
	if h.missedThrough == 0 && len(h.events) < maxHeldEvents {
		h.events = append(h.events, evt)
		return
	}
	if _, logID, ok := events.MessagePosition(evt); ok {
		h.missedThrough = max(h.missedThrough, logID)
	}
}

// hold starts holding chatID with its undelivered events, in source order.
func (kc *KakaoClient) hold(chatID int64, pending []events.Event) {
	held := &heldChat{}
	for _, evt := range pending {
		held.add(evt)
	}
	kc.holds.chats[chatID] = held
	kc.log().Warn().Int64("kakao_chat_id", chatID).Msg("Kakao chat delivery failed; holding the chat and retrying without reconnecting")
	kc.scheduleHeld(chatID, held)
}

// scheduleHeld sets a held chat's next timed retry, or pauses it.
func (kc *KakaoClient) scheduleHeld(chatID int64, held *heldChat) {
	held.due = time.Time{}
	if kc.checkSourceAccess(context.Background(), chatID) != nil {
		// Its source access state reports itself; a retry waits for it to
		// change.
		return
	}
	if held.attempts < len(kc.deliveryRetryDelays) {
		held.due = time.Now().Add(kc.deliveryRetryDelays[held.attempts])
		return
	}
	if !held.paused {
		held.paused = true
		kc.pausedDeliveries.Add(1)
		kc.log().Warn().Int64("kakao_chat_id", chatID).Int("attempts", held.attempts).Msg("Kakao chat delivery paused after repeated failures")
		kc.sendReadyState()
	}
}

// nextHeldRetry returns the earliest timed retry, if any.
func (kc *KakaoClient) nextHeldRetry() (time.Time, bool) {
	var next time.Time
	for _, held := range kc.holds.chats {
		if !held.due.IsZero() && (next.IsZero() || held.due.Before(next)) {
			next = held.due
		}
	}
	return next, !next.IsZero()
}

// retryDueHolds retries every held chat whose timed retry is due.
func (kc *KakaoClient) retryDueHolds(c kakaoClient, now time.Time) {
	for chatID, held := range kc.holds.chats {
		if !held.due.IsZero() && !held.due.After(now) {
			kc.retryHeld(c, chatID)
		}
	}
}

// resumeHeld retries held chats after a resume trigger, restarting their
// retry schedule. chatID 0 resumes every chat that waits for a trigger.
func (kc *KakaoClient) resumeHeld(c kakaoClient, chatID int64) {
	for id, held := range kc.holds.chats {
		if (chatID == 0 || id == chatID) && held.due.IsZero() {
			held.attempts = 0
			kc.retryHeld(c, id)
		}
	}
}

// retryHeld delivers a held chat's buffer in order. A failure keeps the rest
// held and schedules the next attempt; full delivery releases the chat.
func (kc *KakaoClient) retryHeld(c kakaoClient, chatID int64) {
	held := kc.holds.chats[chatID]
	for {
		for len(held.events) > 0 {
			if !kc.handleEvent(c, held.events[0]) {
				held.attempts++
				kc.scheduleHeld(chatID, held)
				return
			}
			held.events = held.events[1:]
		}
		if held.missedThrough == 0 {
			break
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		missed, err := c.CatchUp(ctx, chatID, held.missedThrough)
		cancel()
		if err != nil {
			kc.log().Warn().Str("error_class", sourceErrorClass(err)).Int64("kakao_chat_id", chatID).Msg("Could not read the dropped interval of a held Kakao chat")
			held.attempts++
			kc.scheduleHeld(chatID, held)
			return
		}
		held.events, held.missedThrough = missed, 0
	}
	delete(kc.holds.chats, chatID)
	kc.log().Info().Int64("kakao_chat_id", chatID).Msg("Held Kakao chat delivered")
	if held.paused && kc.pausedDeliveries.Add(-1) == 0 {
		kc.sendReadyState()
	}
}

// requestDeliveryResume asks the event pump to retry paused chats. It
// reports whether any chat was paused.
func (kc *KakaoClient) requestDeliveryResume() bool {
	if kc.pausedDeliveries.Load() == 0 {
		return false
	}
	select {
	case kc.deliveryResume <- struct{}{}:
	default:
	}
	return true
}
