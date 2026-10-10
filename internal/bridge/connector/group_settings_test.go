package connector

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
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

type powerIntent struct {
	announcementIntent
	powers []*event.PowerLevelsEventContent
}

func (i *powerIntent) SendState(ctx context.Context, room id.RoomID, typ event.Type, key string, content *event.Content, ts time.Time) (*mautrix.RespSendEvent, error) {
	if typ == event.StatePowerLevels {
		i.powers = append(i.powers, content.Parsed.(*event.PowerLevelsEventContent))
		return &mautrix.RespSendEvent{EventID: "$power:test"}, nil
	}
	return i.announcementIntent.SendState(ctx, room, typ, key, content, ts)
}

// Regular groups have no roles. Raising a KakaoTalk member's power level has
// no source meaning: it is rejected once and the previous levels restored.
// Levels of Matrix-only users stay a Matrix matter.
func TestPowerLevelChangesForKakaoMembersAreRejectedAndRestored(t *testing.T) {
	ctx := context.Background()
	raw, err := dbutil.NewWithDialect(filepath.Join(t.TempDir(), "bridge.db"), "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.RawDB.Close() }()
	bot := &powerIntent{}
	br, err := newFrameworkConversionBridge(ctx, raw, bot)
	if err != nil {
		t.Fatal(err)
	}
	portal, err := br.GetPortalByKey(ctx, makePortalKey(testChatID, makeUserLoginID(testSelfID)))
	if err != nil {
		t.Fatal(err)
	}
	kc := newKakaoClient(&bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: makeUserLoginID(testSelfID), Metadata: &UserLoginMetadata{}}, Bridge: br, Log: zerolog.Nop()}, testSelfID, nil)
	kc.client = &fakeKakao{}
	previous := &event.PowerLevelsEventContent{Users: map[id.UserID]int{"@owner:test": 100}}
	change := func(target bridgev2.GhostOrUserLogin) *bridgev2.MatrixPowerLevelChange {
		msg := &bridgev2.MatrixPowerLevelChange{Users: map[id.UserID]*bridgev2.UserPowerLevelChange{
			"@target:test": {Target: target, SinglePowerLevelChange: bridgev2.SinglePowerLevelChange{OrigLevel: 0, NewLevel: 50, NewIsSet: true}},
		}}
		msg.Event = &event.Event{Type: event.StatePowerLevels}
		msg.Content = &event.PowerLevelsEventContent{Users: map[id.UserID]int{"@owner:test": 100, "@target:test": 50}}
		msg.PrevContent = previous
		msg.Portal = portal
		return msg
	}
	ghost := &bridgev2.Ghost{Ghost: &database.Ghost{ID: makeUserID(2000)}}
	changed, err := kc.HandleMatrixPowerLevels(ctx, change(ghost))
	var status bridgev2.MessageStatus
	if changed || !errors.As(err, &status) || status.Status != event.MessageStatusFail || !status.IsCertain || !status.SendNotice {
		t.Fatalf("ghost change: changed=%v err=%v", changed, err)
	}
	if len(bot.powers) != 1 || bot.powers[0].Users["@target:test"] != 0 || bot.powers[0].Users["@owner:test"] != 100 {
		t.Fatalf("restored levels = %+v", bot.powers)
	}
	bot.powers = nil
	if changed, err := kc.HandleMatrixPowerLevels(ctx, change(nil)); changed || err != nil || len(bot.powers) != 0 {
		t.Fatalf("Matrix-only change: changed=%v err=%v restores=%d", changed, err, len(bot.powers))
	}
}
