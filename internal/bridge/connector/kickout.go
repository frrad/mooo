package connector

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2/status"
)

// kickoutRecord is the persisted KICKOUT notice for a login.
type kickoutRecord struct {
	Reason int32 `json:"reason"`
	// At is the Unix time in milliseconds when the notice arrived.
	At int64 `json:"at"`
}

const kickoutSaveTimeout = 5 * time.Second

func kickedOutState(reason int32) status.BridgeState {
	return status.BridgeState{
		StateEvent: status.StateBadCredentials,
		Error:      stateKickedOut,
		Message:    fmt.Sprintf("KakaoTalk signed this device out (reason %d). Log out of the bridge and log in again.", reason),
	}
}

// kickedOut reports a persisted KICKOUT notice for this login.
func (kc *KakaoClient) kickedOut() (int32, bool) {
	meta, ok := kc.login.Metadata.(*UserLoginMetadata)
	if !ok || meta.KickedOut == nil {
		return 0, false
	}
	return meta.KickedOut.Reason, true
}

// recordKickout persists a KICKOUT notice so that a restart does not log in
// again with the same credentials. The official client logs out on KICKOUT.
// A failed save is logged; this process still stops recovery.
func (kc *KakaoClient) recordKickout(reason int32) {
	meta, ok := kc.login.Metadata.(*UserLoginMetadata)
	if !ok {
		kc.log().Error().Msg("Cannot record KakaoTalk kickout: login has no Kakao metadata")
		return
	}
	meta.KickedOut = &kickoutRecord{Reason: reason, At: kc.now().UnixMilli()}
	ctx, cancel := context.WithTimeout(context.Background(), kickoutSaveTimeout)
	defer cancel()
	if err := kc.login.Save(ctx); err != nil {
		kc.log().Err(err).Msg("Failed to record KakaoTalk kickout; a restart would reconnect")
	}
}
