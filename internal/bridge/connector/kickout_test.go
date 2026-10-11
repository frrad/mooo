package connector

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/status"

	"github.com/frrad/mooo/internal/protocol/events"
)

// The status message follows the Mac client's logout alert (static trace,
// macOS 26.8.0): reason 0 always shows the "logged in on another device"
// text, another reason shows the server's errMsg when present and a generic
// list of causes otherwise, and the link is offered only with both errUrl and
// errUrlLabel.
func TestKickedOutStateFollowsOfficialAlertText(t *testing.T) {
	for _, tc := range []struct {
		name        string
		notice      events.Kickout
		contains    []string
		notContains []string
	}{
		{
			name:        "reason 0 ignores server text",
			notice:      events.Kickout{Reason: 0, Message: "Synthetic server text"},
			contains:    []string{"logged in on another device", "reason 0"},
			notContains: []string{"Synthetic server text"},
		},
		{
			name:     "server text for another reason",
			notice:   events.Kickout{Reason: 3, Message: "Synthetic server text"},
			contains: []string{"Synthetic server text", "reason 3"},
		},
		{
			name:        "generic causes without server text",
			notice:      events.Kickout{Reason: 3},
			contains:    []string{"password", "reason 3"},
			notContains: []string{"logged in on another device."},
		},
		{
			name:     "link with label",
			notice:   events.Kickout{Reason: 3, Message: "Synthetic server text", URL: "https://example.test/help", URLLabel: "Synthetic help"},
			contains: []string{"Synthetic help: https://example.test/help"},
		},
		{
			name:        "link without label is not offered",
			notice:      events.Kickout{Reason: 3, Message: "Synthetic server text", URL: "https://example.test/help"},
			notContains: []string{"https://example.test/help"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := kickedOutState(newKickoutRecord(tc.notice, time.Unix(0, 0)))
			if state.StateEvent != status.StateBadCredentials || state.Error != stateKickedOut {
				t.Fatalf("state = %+v, want BAD_CREDENTIALS %s", state, stateKickedOut)
			}
			if !strings.Contains(state.Message, "Log out of the bridge and log in again") {
				t.Fatalf("message %q lacks the operator instruction", state.Message)
			}
			for _, want := range tc.contains {
				if !strings.Contains(state.Message, want) {
					t.Errorf("message %q lacks %q", state.Message, want)
				}
			}
			for _, unwanted := range tc.notContains {
				if strings.Contains(state.Message, unwanted) {
					t.Errorf("message %q contains %q", state.Message, unwanted)
				}
			}
		})
	}
}

func TestKickoutTextSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	first := &fakeKakao{stream: make(chan events.Result, 1)}
	var opens atomic.Int32
	kc, harness, br := newFrameworkTestClient(t, func() (kakaoClient, error) {
		opens.Add(1)
		return first, nil
	})
	kc.wait = func(context.Context, time.Duration) error { return nil }
	kc.Connect(ctx)
	first.stream <- events.Result{Event: events.Kickout{Reason: 3, Message: "Synthetic server text", URL: "https://example.test/help", URLLabel: "Synthetic help"}}
	close(first.stream)
	waitFor(t, func() bool { return harness.lastState().StateEvent == status.StateBadCredentials })
	live := harness.lastState().Message
	kc.Disconnect()

	reloaded, err := br.DB.UserLogin.GetByID(ctx, kc.login.ID)
	if err != nil {
		t.Fatal(err)
	}
	restartedLogin := &bridgev2.UserLogin{UserLogin: reloaded, Bridge: br, User: kc.login.User, Log: zerolog.Nop()}
	restarted := newKakaoClient(restartedLogin, newBridgeStore(br.DB), testSelfID, func() (kakaoClient, error) {
		t.Error("restarted bridge opened a kicked-out profile")
		return first, nil
	})
	restartedHarness := &testHarness{}
	restarted.sendState = restartedHarness.sendState
	restarted.Connect(ctx)
	if got := restartedHarness.lastState().Message; got != live || !strings.Contains(got, "Synthetic server text") || !strings.Contains(got, "https://example.test/help") {
		t.Fatalf("restarted message = %q, want the live message %q", got, live)
	}
	restarted.Disconnect()
	if got := opens.Load(); got != 1 {
		t.Fatalf("opens = %d, want 1", got)
	}
}
