package events

import (
	"testing"

	"github.com/frrad/mooo/internal/protocol/loco"
)

func TestMembershipPushMethodsRemainUnknownUntilContractIsComplete(t *testing.T) {
	// These methods are present in the official client's unsolicited-notice
	// inventory, but their complete public wire/persistence contracts are not
	// established yet. Preserve them as observable unknown packets rather than
	// guessing at member mutations.
	for _, method := range []string{"NEWMEM", "CHGCHATST", "CHGMETA", "CHGMCMETA", "LEFT"} {
		t.Run(method, func(t *testing.T) {
			event, err := Decode(loco.Packet{Header: loco.Header{Method: method}})
			unknown, ok := event.(UnknownPacket)
			if err != nil || !ok || unknown.Method != method {
				t.Fatalf("event=%#v (%T), error=%v; want UnknownPacket(%q)", event, event, err, method)
			}
		})
	}
}
