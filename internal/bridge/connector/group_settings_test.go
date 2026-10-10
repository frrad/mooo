package connector

import (
	"context"
	"testing"
	"time"

	"maunium.net/go/mautrix/event"

)

// Observed (owned, 2026-10-10): turning a group's notifications off on the
// account's phone sends no push, but the secondary session sees p=false in
// LOGINLIST and CHATINFO. It is a personal setting: it maps to the Matrix
// user's mute only and never to shared room state.
func TestPersonalNotificationSettingMapsToMuteOnly(t *testing.T) {
	cases := []struct {
		label     string
		set, push bool
		wantMuted *bool
	}{
		{"notifications off", true, false, ptrBool(true)},
		{"notifications on", true, true, ptrBool(false)},
		{"not reported", false, false, nil},
	}
	var names []string
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			kc, backend, _ := newGroupCreationFramework(t)
			ctx := context.Background()
			p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
			if err != nil {
				t.Fatal(err)
			}
			backend.chatInfo.ChatData.PushAlert = tc.push
			backend.chatInfo.ChatData.PushAlertSet = tc.set
			info, err := kc.chatInfoFromClient(ctx, p, backend, true)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantMuted == nil {
				if info.UserLocal != nil && info.UserLocal.MutedUntil != nil {
					t.Fatal("unreported setting changed the mute")
				}
			} else {
				if info.UserLocal == nil || info.UserLocal.MutedUntil == nil {
					t.Fatal("personal notification setting not mapped")
				}
				muted := info.UserLocal.MutedUntil.After(time.Now())
				if muted != *tc.wantMuted {
					t.Fatalf("muted = %v", muted)
				}
				if muted && !info.UserLocal.MutedUntil.Equal(event.MutedForever) {
					t.Fatalf("mute until %v, want indefinite", info.UserLocal.MutedUntil)
				}
			}
			if info.Name == nil {
				t.Fatal("shared name missing")
			}
			names = append(names, *info.Name)
			for _, member := range info.Members.MemberMap {
				if member.PowerLevel != nil {
					t.Fatalf("member %s given a power level; regular groups have no roles", member.Sender)
				}
			}
		})
	}
	if len(names) == 3 && (names[0] != names[1] || names[1] != names[2]) {
		t.Fatalf("personal setting changed the shared name: %q", names)
	}
}

func ptrBool(v bool) *bool { return &v }
