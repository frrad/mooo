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

type membershipIntent struct {
	powerIntent
	mxid    id.UserID
	members []string
	invited []id.UserID
	joined  int
}

func (i *membershipIntent) GetMXID() id.UserID { return i.mxid }

func (i *membershipIntent) SendState(ctx context.Context, room id.RoomID, typ event.Type, key string, content *event.Content, ts time.Time) (*mautrix.RespSendEvent, error) {
	if typ == event.StateMember {
		i.members = append(i.members, key+":"+string(content.Parsed.(*event.MemberEventContent).Membership))
		return &mautrix.RespSendEvent{EventID: "$member:test"}, nil
	}
	return i.powerIntent.SendState(ctx, room, typ, key, content, ts)
}

func (i *membershipIntent) EnsureInvited(_ context.Context, _ id.RoomID, user id.UserID) error {
	i.invited = append(i.invited, user)
	return nil
}

func (i *membershipIntent) EnsureJoined(context.Context, id.RoomID, ...bridgev2.EnsureJoinedParams) error {
	i.joined++
	return nil
}

// Regular groups have no kick. A Matrix kick or ban of a KakaoTalk member is
// rejected once with a notice and the member's Matrix membership is put back;
// the source is untouched. Invites are covered in group_matrix_invite_test.go.
// Matrix-only and self membership changes pass without a notice.
func TestMatrixMembershipChangesForKakaoMembersAreRejectedAndRestored(t *testing.T) {
	ctx := context.Background()
	raw, err := dbutil.NewWithDialect(filepath.Join(t.TempDir(), "bridge.db"), "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.RawDB.Close() }()
	bot := &membershipIntent{mxid: "@bot:test"}
	br, err := newFrameworkConversionBridge(ctx, raw, bot)
	if err != nil {
		t.Fatal(err)
	}
	portal, err := br.GetPortalByKey(ctx, makePortalKey(testChatID, makeUserLoginID(testSelfID)))
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeKakao{}
	kc := newKakaoClient(&bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: makeUserLoginID(testSelfID), Metadata: &UserLoginMetadata{}}, Bridge: br, Log: zerolog.Nop()}, testSelfID, nil)
	kc.client = fake
	ghostIntent := &membershipIntent{mxid: "@kakao_2000:test"}
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
		bot.members, bot.invited, ghostIntent.joined = nil, nil, 0
		_, err := kc.HandleMatrixMembership(ctx, change(tc.typ, ghost))
		var status bridgev2.MessageStatus
		if !errors.As(err, &status) || status.Status != event.MessageStatusFail || !status.IsCertain || !status.SendNotice {
			t.Fatalf("%v: err = %v", tc.typ, err)
		}
		if len(bot.members) != len(tc.wantMembers) || (len(tc.wantMembers) > 0 && bot.members[0] != tc.wantMembers[0]) {
			t.Fatalf("%v: member state = %q", tc.typ, bot.members)
		}
		rejoined := len(bot.invited) == 1 && bot.invited[0] == "@kakao_2000:test" && ghostIntent.joined == 1
		if rejoined != tc.wantRejoin {
			t.Fatalf("%v: rejoined = %v (invited %q, joined %d)", tc.typ, rejoined, bot.invited, ghostIntent.joined)
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
