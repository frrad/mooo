// Command mooo-bridge runs the KakaoTalk Matrix bridge.
package main

import (
	"maunium.net/go/mautrix/bridgev2/matrix/mxmain"

	"github.com/frrad/mooo/internal/bridge/connector"
	"github.com/frrad/mooo/internal/buildinfo"
)

func main() {
	m := mxmain.BridgeMain{
		Name:        "mooo-bridge",
		URL:         "https://github.com/frrad/mooo",
		Description: "A Matrix-KakaoTalk puppeting bridge.",
		Version:     buildinfo.Version,
		Connector:   &connector.KakaoConnector{},
	}
	m.PostInit = func() {
		// Remote events must be handled inline so their results can gate
		// continuity commits; see connector.ErrUnsafeEventDelivery.
		if m.Config.Bridge.PortalEventBuffer != 0 || m.Config.Bridge.AsyncEvents {
			m.Log.Warn().Msg("Overriding bridge.portal_event_buffer to 0 and bridge.async_events to false")
		}
		m.Config.Bridge.PortalEventBuffer = 0
		m.Config.Bridge.AsyncEvents = false
	}
	m.Run()
}
