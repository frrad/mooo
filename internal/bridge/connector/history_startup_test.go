package connector

import (
	"errors"
	"slices"
	"testing"

	"github.com/frrad/mooo/internal/protocol/events"
)

// A connect sends one MCHATLOGS chain. With startup history it runs before
// downstream bootstrap and carries the acquisition; its failure stops the
// bootstrap before live subscription.
func TestBootstrapStartupHistoryRidesTheOneCatchUpChain(t *testing.T) {
	for _, mode := range []string{"disabled", "enabled", "error"} {
		t.Run(mode, func(t *testing.T) {
			kc, _, _ := newGroupCreationFramework(t)
			kc.login.Bridge.Network = &KakaoConnector{Config: Config{StartupHistory: mode != "disabled"}}
			fake := &fakeKakao{stream: make(chan events.Result)}
			if mode == "error" {
				fake.resumeErr = errors.New("synthetic acquisition failure")
			}
			_, err := kc.connectAndSubscribe(t.Context(), fake)
			if (err != nil) != (mode == "error") {
				t.Fatal(mode, err)
			}
			chains := 0
			for _, call := range fake.calls {
				if call == "CatchUpAll" {
					chains++
				}
			}
			if chains != 1 {
				t.Fatalf("%s: %d chains, calls %v", mode, chains, fake.calls)
			}
			withHistory := slices.Contains(fake.calls, "StartupHistory")
			if withHistory != (mode != "disabled") {
				t.Fatal(mode, fake.calls)
			}
			if withHistory && slices.Index(fake.calls, "CatchUpAll") != 0 {
				t.Fatal("acquisition followed downstream bootstrap", fake.calls)
			}
			if mode == "error" && slices.Contains(fake.calls, "Events") {
				t.Fatal("subscribed after failed acquisition")
			}
		})
	}
}
