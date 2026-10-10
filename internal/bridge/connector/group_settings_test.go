package connector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
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
			info, _, err := kc.chatInfoFromClient(ctx, p, backend, chatInfoOptions{regularGroupOnly: true})
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

// Regular groups have no roles. Raising a KakaoTalk member's power level has
// no source meaning: it is rejected once and the previous levels restored.
// Levels of Matrix-only users stay a Matrix matter.
func TestPowerLevelChangesForKakaoMembersAreRejectedAndRestored(t *testing.T) {
	ctx := context.Background()
	bot := &recordingIntent{}
	br, _ := newFrameworkBridge(t, frameworkOpts{intent: bot})
	restored := func() []*event.PowerLevelsEventContent {
		var out []*event.PowerLevelsEventContent
		for _, s := range bot.sentStates(event.StatePowerLevels) {
			out = append(out, s.content.Parsed.(*event.PowerLevelsEventContent))
		}
		return out
	}
	portal, err := br.GetPortalByKey(ctx, makePortalKey(testChatID, makeUserLoginID(testSelfID)))
	if err != nil {
		t.Fatal(err)
	}
	kc := newKakaoClient(&bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: makeUserLoginID(testSelfID), Metadata: &UserLoginMetadata{}}, Bridge: br, Log: zerolog.Nop()}, newBridgeStore(br.DB), testSelfID, nil)
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
	if powers := restored(); len(powers) != 1 || powers[0].Users["@target:test"] != 0 || powers[0].Users["@owner:test"] != 100 {
		t.Fatalf("restored levels = %+v", powers)
	}
	bot.reset()
	if changed, err := kc.HandleMatrixPowerLevels(ctx, change(nil)); changed || err != nil || len(restored()) != 0 {
		t.Fatalf("Matrix-only change: changed=%v err=%v restores=%d", changed, err, len(restored()))
	}
}

// Regular groups have no kick. A Matrix kick or ban of a KakaoTalk member is
// rejected once with a notice and the member's Matrix membership is put back;
// the source is untouched. Invites are covered in group_matrix_invite_test.go.
// Matrix-only and self membership changes pass without a notice.
func TestMatrixMembershipChangesForKakaoMembersAreRejectedAndRestored(t *testing.T) {
	ctx := context.Background()
	bot := &recordingIntent{}
	br, _ := newFrameworkBridge(t, frameworkOpts{intent: bot})
	portal, err := br.GetPortalByKey(ctx, makePortalKey(testChatID, makeUserLoginID(testSelfID)))
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeKakao{}
	kc := newKakaoClient(&bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: makeUserLoginID(testSelfID), Metadata: &UserLoginMetadata{}}, Bridge: br, Log: zerolog.Nop()}, newBridgeStore(br.DB), testSelfID, nil)
	kc.client = fake
	ghostIntent := &recordingIntent{mxid: "@kakao_2000:test"}
	ghost := &bridgev2.Ghost{Ghost: &database.Ghost{ID: makeUserID(2000)}, Intent: ghostIntent}
	change := func(typ bridgev2.MembershipChangeType, target bridgev2.GhostOrUserLogin) *bridgev2.MatrixMembershipChange {
		msg := &bridgev2.MatrixMembershipChange{Target: target, Type: typ}
		msg.Event = &event.Event{Type: event.StateMember}
		msg.Content = &event.MemberEventContent{Membership: typ.To}
		msg.Portal = portal
		return msg
	}
	for _, tc := range []struct {
		typ         bridgev2.MembershipChangeType
		wantMembers []string
		wantRejoin  bool
	}{
		{bridgev2.Kick, nil, true},
		{bridgev2.BanJoined, []string{"@kakao_2000:test:leave"}, true},
		{bridgev2.BanLeft, []string{"@kakao_2000:test:leave"}, false},
	} {
		bot.reset()
		ghostIntent.reset()
		_, err := kc.HandleMatrixMembership(ctx, change(tc.typ, ghost))
		var status bridgev2.MessageStatus
		if !errors.As(err, &status) || status.Status != event.MessageStatusFail || !status.IsCertain || !status.SendNotice {
			t.Fatalf("%v: err = %v", tc.typ, err)
		}
		members := bot.memberChanges()
		if len(members) != len(tc.wantMembers) || (len(tc.wantMembers) > 0 && members[0] != tc.wantMembers[0]) {
			t.Fatalf("%v: member state = %q", tc.typ, members)
		}
		invited := bot.invites()
		rejoined := len(invited) == 1 && invited[0] == "@kakao_2000:test" && ghostIntent.joins() == 1
		if rejoined != tc.wantRejoin {
			t.Fatalf("%v: rejoined = %v (invited %q, joined %d)", tc.typ, rejoined, invited, ghostIntent.joins())
		}
	}
	for _, typ := range []bridgev2.MembershipChangeType{bridgev2.Leave, bridgev2.Join, bridgev2.ProfileChange} {
		if _, err := kc.HandleMatrixMembership(ctx, change(typ, &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: makeUserLoginID(testSelfID)}})); err != nil {
			t.Fatalf("self %v rejected: %v", typ, err)
		}
	}
	if len(fake.calls) != 0 {
		t.Fatalf("membership changes reached KakaoTalk: %q", fake.calls)
	}
}

// The Mac client keeps per-chat notifications local and sends no request; a
// Matrix mute is likewise the Matrix user's own setting and never reaches
// KakaoTalk or shared room state.
func TestMatrixMuteStaysLocal(t *testing.T) {
	fake := &fakeKakao{}
	kc := connectedClient(t, fake)
	msg := &bridgev2.MatrixMute{}
	msg.Content = &event.BeeperMuteEventContent{MutedUntil: -1}
	msg.Portal = &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, "1000")}}
	before := len(fake.calls)
	if err := kc.HandleMute(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != before || len(fake.sends) != 0 {
		t.Fatalf("mute reached KakaoTalk: %q", fake.calls[before:])
	}
}
