package connector

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2/status"

	"github.com/frrad/mooo/internal/protocol/events"
)

// kickoutRecord is the persisted KICKOUT notice for a login, including the
// server's optional explanation text so a restart reports the same message.
type kickoutRecord struct {
	Reason   int32  `json:"reason"`
	Message  string `json:"message,omitempty"`
	URL      string `json:"url,omitempty"`
	URLLabel string `json:"url_label,omitempty"`
	// At is the Unix time in milliseconds when the notice arrived.
	At int64 `json:"at"`
}

func newKickoutRecord(notice events.Kickout, at time.Time) kickoutRecord {
	return kickoutRecord{Reason: notice.Reason, Message: notice.Message, URL: notice.URL, URLLabel: notice.URLLabel, At: at.UnixMilli()}
}

// Bridge wording for the Mac alert's two localized defaults.
const (
	kickoutOtherDeviceText = "KakaoTalk logged this device out because the account logged in on another device."
	kickoutGenericText     = "KakaoTalk logged this device out. Possible causes: a logout from the phone, a login on another device, unregistering, account deletion or a password change."
)

// kickedOutState follows the Mac client's logout alert. Reason 0 always
// shows the other-device text, even when the server sent errMsg. Any other
// reason shows errMsg verbatim, or the generic causes when it is empty. The
// link is offered only when both errUrl and errUrlLabel are present, as the
// Mac shows it only as a button labelled errUrlLabel.
func kickedOutState(record kickoutRecord) status.BridgeState {
	text := kickoutGenericText
	switch {
	case record.Reason == 0:
		text = kickoutOtherDeviceText
	case record.Message != "":
		text = record.Message
	}
	if record.URL != "" && record.URLLabel != "" {
		text += " " + record.URLLabel + ": " + record.URL
	}
	return status.BridgeState{
		StateEvent: status.StateBadCredentials,
		Error:      stateKickedOut,
		Message:    fmt.Sprintf("%s (KakaoTalk reason %d.) Log out of the bridge and log in again.", text, record.Reason),
	}
}

// kickedOut returns the persisted KICKOUT notice for this login, if any.
func (kc *KakaoClient) kickedOut() (kickoutRecord, bool) {
	kc.metaMu.Lock()
	defer kc.metaMu.Unlock()
	meta, ok := kc.login.Metadata.(*UserLoginMetadata)
	if !ok || meta.KickedOut == nil {
		return kickoutRecord{}, false
	}
	return *meta.KickedOut, true
}

// recordKickout persists a KICKOUT notice so that a restart does not log in
// again with the same credentials. The official client logs out on KICKOUT.
// A failed save is logged; this process still stops recovery.
func (kc *KakaoClient) recordKickout(record kickoutRecord) {
	kc.saveLoginMetadata("kickout", func(meta *UserLoginMetadata) {
		meta.KickedOut = &record
	})
}

const loginSaveTimeout = 5 * time.Second

// saveLoginMetadata applies update to the login's metadata and saves it. A
// failed save is logged; the in-memory state still applies to this process.
func (kc *KakaoClient) saveLoginMetadata(what string, update func(*UserLoginMetadata)) {
	kc.metaMu.Lock()
	defer kc.metaMu.Unlock()
	meta, ok := kc.login.Metadata.(*UserLoginMetadata)
	if !ok {
		kc.log().Error().Str("record", what).Msg("Cannot save login state: login has no Kakao metadata")
		return
	}
	update(meta)
	ctx, cancel := context.WithTimeout(context.Background(), loginSaveTimeout)
	defer cancel()
	if err := kc.saveLogin(ctx); err != nil {
		kc.log().Err(err).Str("record", what).Msg("Failed to save login state; a restart would not see it")
	}
}
