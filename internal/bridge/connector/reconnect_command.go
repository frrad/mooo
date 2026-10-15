package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2/commands"
)

var commandReconnect = &commands.FullHandler{
	Name:          "reconnect",
	Help:          commands.HelpMeta{Section: commands.HelpSectionAuth, Description: "Reconnect a stopped Kakao login after dealing with the reason it stopped"},
	RequiresLogin: true,
	Func: func(ce *commands.Event) {
		login := ce.User.GetDefaultLogin()
		if login == nil {
			ce.Reply("No Kakao profile is configured")
			return
		}
		kc, ok := login.Client.(*KakaoClient)
		if !ok || login.UserMXID != ce.User.MXID {
			ce.Reply("Select your own Kakao login before reconnecting")
			return
		}
		switch kc.operatorReconnect() {
		case reconnectKickedOut:
			ce.Reply("KakaoTalk signed this device out. Log out of the bridge and log in again; reconnecting would reuse the revoked session.")
		case reconnectAlreadyConnected:
			ce.Reply("The Kakao login is already connected.")
		case reconnectResumingDelivery:
			ce.Reply("The Kakao login is connected. Retrying delivery of the paused chats; the bridge state reports the result.")
		case reconnectPending:
			ce.Reply("A reconnect is already in progress or scheduled. The bridge state shows when it will try again.")
		case reconnectStarted:
			ce.Reply("Reconnecting to KakaoTalk. The bridge state reports the result.")
		}
	},
}

type reconnectResult int

const (
	reconnectStarted reconnectResult = iota
	reconnectAlreadyConnected
	reconnectPending
	reconnectKickedOut
	reconnectResumingDelivery
)

// operatorReconnect starts a new session for a login that stopped without a
// scheduled retry, such as after a login rejection or an unidentifiable
// message. It is the explicit operator action that continuity stops require;
// it never shortens a scheduled retry, which may be waiting out a rate limit.
func (kc *KakaoClient) operatorReconnect() reconnectResult {
	if _, kicked := kc.kickedOut(); kicked {
		return reconnectKickedOut
	}
	kc.mu.Lock()
	connected := kc.client != nil
	pending := kc.connecting || kc.retry.running()
	kc.mu.Unlock()
	switch {
	case connected && kc.requestDeliveryResume():
		// A paused chat is retried in the live session; no new login.
		return reconnectResumingDelivery
	case connected:
		return reconnectAlreadyConnected
	case pending:
		return reconnectPending
	}
	// Disconnect finishes any retained owner cleanup first, so the new
	// session cannot share the profile with the old one.
	kc.Disconnect()
	go kc.Connect(context.Background())
	return reconnectStarted
}
