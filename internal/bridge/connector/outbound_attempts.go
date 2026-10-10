package connector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// A homeserver redelivers an appservice transaction it did not see
// acknowledged, and the SDK hands every copy of the event to the connector
// before any of them is saved. Each Matrix event therefore reserves a durable
// attempt before its single Kakao send; a later copy, including one after a
// restart, is never sent.
const outboundAttemptRetention = 30 * 24 * time.Hour

var errOutboundAlreadyAttempted = errors.New("connector: Matrix event was already sent to KakaoTalk")

func (kc *KakaoClient) reserveOutboundKV(ctx context.Context, eventID id.EventID) (bool, error) {
	if kc.login == nil || kc.login.Bridge == nil || kc.login.Bridge.DB == nil {
		return false, errors.New("connector: outbound attempt store unavailable")
	}
	kv := newKVStore(kc.login.Bridge.DB.KV)
	prefix := fmt.Sprintf("kakao:outbound-attempt:%s:", kc.login.ID)
	now := time.Now()
	if err := kv.deleteOlderThan(ctx, prefix, kvTimeStamp(now.Add(-outboundAttemptRetention))); err != nil {
		return false, err
	}
	return kv.putIfAbsent(ctx, prefix+string(eventID), kvTimeStamp(now))
}

// beginOutbound must be called immediately before the one source send.
func (kc *KakaoClient) beginOutbound(ctx context.Context, msg *bridgev2.MatrixMessage) error {
	return kc.beginOutboundEvent(ctx, msg.Event)
}

// beginOutboundEvent reserves any Matrix event (message, edit or redaction)
// immediately before its one source request.
func (kc *KakaoClient) beginOutboundEvent(ctx context.Context, evt *event.Event) error {
	if evt == nil || evt.ID == "" {
		return errors.New("connector: Matrix event has no identity to deduplicate its send")
	}
	reserved, err := kc.reserveOutbound(ctx, evt.ID)
	if err != nil {
		return fmt.Errorf("connector: record outbound attempt: %w", err)
	}
	if !reserved {
		status := bridgev2.WrapErrorInStatus(errOutboundAlreadyAttempted).
			WithStatus(event.MessageStatusFail).
			WithIsCertain(false)
		// The first copy owns this event's status; a duplicate must not
		// overwrite its delivered or unconfirmed result.
		status.DisableMSS = true
		return status
	}
	return nil
}
