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
	db := kc.login.Bridge.DB.KV
	prefix := fmt.Sprintf("kakao:outbound-attempt:%s:", kc.login.ID)
	now := time.Now()
	// Fixed-width seconds keep the stored value lexically ordered.
	stamp := func(t time.Time) string { return fmt.Sprintf("%020d", t.Unix()) }
	if _, err := db.Exec(ctx, "DELETE FROM kv_store WHERE bridge_id=$1 AND key LIKE $2 AND value < $3", db.BridgeID, prefix+"%", stamp(now.Add(-outboundAttemptRetention))); err != nil {
		return false, err
	}
	result, err := db.Exec(ctx, "INSERT INTO kv_store (bridge_id,key,value) VALUES ($1,$2,$3) ON CONFLICT (bridge_id,key) DO NOTHING", db.BridgeID, prefix+string(eventID), stamp(now))
	if err != nil {
		return false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return inserted == 1, nil
}

// beginOutbound must be called immediately before the one source send.
func (kc *KakaoClient) beginOutbound(ctx context.Context, msg *bridgev2.MatrixMessage) error {
	if msg.Event == nil || msg.Event.ID == "" {
		return errors.New("connector: Matrix event has no identity to deduplicate its send")
	}
	reserved, err := kc.reserveOutbound(ctx, msg.Event.ID)
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
