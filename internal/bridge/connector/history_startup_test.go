package connector

import (
	"context"
	"errors"
	"testing"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/events"
)

type startupKakao struct {
	*fakeKakao
	startupErr error
}

func (f *startupKakao) AcquireStartupHistory(context.Context, client.StartupHistoryOptions) (client.StartupHistoryResult, error) {
	f.calls = append(f.calls, "AcquireStartupHistory")
	return client.StartupHistoryResult{}, f.startupErr
}

func TestBootstrapStartupHistoryOptInAndFailureBeforeSubscription(t *testing.T) {
	for _, mode := range []string{"disabled", "enabled", "error", "missing-interface"} {
		t.Run(mode, func(t *testing.T) {
			kc, _, _ := newGroupCreationFramework(t)
			kc.login.Bridge.Network = &KakaoConnector{Config: Config{StartupHistory: mode != "disabled"}}
			fake := &fakeKakao{stream: make(chan events.Result)}
			bulk := &startupKakao{fakeKakao: fake}
			if mode == "error" {
				bulk.startupErr = errors.New("synthetic acquisition failure")
			}
			var api kakaoClient = bulk
			if mode == "missing-interface" {
				api = fake
			}
			_, err := kc.connectAndSubscribe(t.Context(), api)
			wantErr := mode == "error" || mode == "missing-interface"
			if (err != nil) != wantErr {
				t.Fatal(mode, err)
			}
			called := false
			for i, call := range fake.calls {
				if call == "AcquireStartupHistory" {
					called = true
					if i != 0 {
						t.Fatal("acquisition followed downstream bootstrap", fake.calls)
					}
				}
				if call == "Events" && wantErr {
					t.Fatal("subscribed after failed acquisition")
				}
			}
			if called != (mode == "enabled" || mode == "error") {
				t.Fatal(mode, fake.calls)
			}
		})
	}
}
