package connector

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/client"
	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
)

type matrixInviteHarness struct {
	kc        *KakaoClient
	fake      *fakeKakao
	bot       *membershipIntent
	portal    *bridgev2.Portal
	ghost     *bridgev2.Ghost
	refreshes []int64
}

func newMatrixInviteHarness(t *testing.T, fake *fakeKakao) *matrixInviteHarness {
	t.Helper()
	ctx := context.Background()
	raw, err := dbutil.NewWithDialect(filepath.Join(t.TempDir(), "bridge.db"), "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.RawDB.Close() })
	bot := &membershipIntent{mxid: "@bot:test"}
	br, err := newFrameworkConversionBridge(ctx, raw, bot)
	if err != nil {
		t.Fatal(err)
	}
	portal, err := br.GetPortalByKey(ctx, makePortalKey(testChatID, makeUserLoginID(testSelfID)))
	if err != nil {
		t.Fatal(err)
	}
	h := &matrixInviteHarness{fake: fake, bot: bot, portal: portal}
	h.kc = newKakaoClient(&bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: makeUserLoginID(testSelfID), Metadata: &UserLoginMetadata{}}, Bridge: br, Log: zerolog.Nop()}, testSelfID, nil)
	h.kc.client = fake
	h.kc.refreshMembership = func(_ kakaoClient, chatID int64) { h.refreshes = append(h.refreshes, chatID) }
	h.ghost = &bridgev2.Ghost{Ghost: &database.Ghost{ID: makeUserID(testOtherID)}, Intent: &membershipIntent{mxid: "@kakao_2000:test"}}
	return h
}

func (h *matrixInviteHarness) invite(eventID id.EventID) error {
	msg := &bridgev2.MatrixMembershipChange{Target: h.ghost, Type: bridgev2.Invite}
	msg.Event = &event.Event{ID: eventID, Type: event.StateMember}
	msg.Content = &event.MemberEventContent{Membership: event.MembershipInvite}
	msg.Portal = h.portal
	_, err := h.kc.HandleMatrixMembership(context.Background(), msg)
	return err
}

func regularGroupFake() *fakeKakao {
	return &fakeKakao{
		chatInfo:   chatmeta.ChatInfoResponse{ChatData: chatmeta.ChatData{ChatID: testChatID, Type: "MultiChat"}},
		memberList: chatmeta.MemberListResponse{MemberIDs: []int64{testSelfID, 4000}},
	}
}

// Mac 26.8.0 (static): any member of a regular group may invite with one
// ADDMEM {chatId, memberIds}. A Matrix invite of a KakaoTalk user therefore
// sends exactly one ADDMEM for that user and keeps the Matrix invite; the
// fresh source roster, not the reply, then decides the Matrix membership.
func TestMatrixInviteOfKakaoUserSendsOneAddMem(t *testing.T) {
	h := newMatrixInviteHarness(t, regularGroupFake())
	if err := h.invite("$invite:test"); err != nil {
		t.Fatalf("invite rejected: %v", err)
	}
	if len(h.fake.invites) != 1 || h.fake.invites[0].ChatID != testChatID || len(h.fake.invites[0].MemberIDs) != 1 || h.fake.invites[0].MemberIDs[0] != testOtherID {
		t.Fatalf("ADDMEM requests = %+v", h.fake.invites)
	}
	if len(h.bot.members) != 0 {
		t.Fatalf("accepted invite was revoked: %q", h.bot.members)
	}
	if len(h.refreshes) != 1 || h.refreshes[0] != testChatID {
		t.Fatalf("roster refreshes = %v", h.refreshes)
	}
	// A redelivered copy of the same Matrix event never invites again.
	if err := h.invite("$invite:test"); err == nil {
		t.Fatal("redelivered invite reported success")
	}
	if len(h.fake.invites) != 1 {
		t.Fatalf("redelivered invite sent ADDMEM again: %+v", h.fake.invites)
	}
}

// Mac 26.8.0 (static): Loco statuses -402 and -405 translate to the client's
// errors 53 and 54, both shown as the blocked-friend alert; any other failure
// status is a refusal. A refused invite is reported and revoked in Matrix.
func TestMatrixInviteRefusedByKakaoIsReportedAndRevoked(t *testing.T) {
	for _, tc := range []struct {
		status  int32
		message string
	}{
		{-402, "blocked friends list"},
		{-405, "blocked friends list"},
		{-500, "refused the invite (status -500)"},
	} {
		fake := regularGroupFake()
		fake.inviteErr = client.StatusError{Command: chat.AddMembersCommand, Status: tc.status}
		h := newMatrixInviteHarness(t, fake)
		err := h.invite("$invite:test")
		var status bridgev2.MessageStatus
		if !errors.As(err, &status) || status.Status != event.MessageStatusFail || !status.IsCertain || !status.SendNotice || !strings.Contains(status.Message, tc.message) {
			t.Fatalf("status %d: err = %v", tc.status, err)
		}
		if len(h.bot.members) != 1 || h.bot.members[0] != "@kakao_2000:test:leave" {
			t.Fatalf("status %d: refused invite not revoked: %q", tc.status, h.bot.members)
		}
		if len(fake.invites) != 1 || len(h.refreshes) != 0 {
			t.Fatalf("status %d: invites=%d refreshes=%v", tc.status, len(fake.invites), h.refreshes)
		}
	}
}

// A lost reply or a warning leaves the outcome unknown. Nothing is retried
// and the invite is not revoked; the fresh source roster decides it.
func TestMatrixInviteWithUnknownOutcomeDefersToSourceRoster(t *testing.T) {
	for _, tc := range []struct {
		label   string
		resp    chat.AddMembersResponse
		err     error
		message string
	}{
		{"lost reply", chat.AddMembersResponse{}, context.DeadlineExceeded, "did not confirm"},
		{"warning", chat.AddMembersResponse{Warning: "synthetic warning"}, nil, "synthetic warning"},
	} {
		fake := regularGroupFake()
		fake.inviteResp, fake.inviteErr = tc.resp, tc.err
		h := newMatrixInviteHarness(t, fake)
		err := h.invite("$invite:test")
		var status bridgev2.MessageStatus
		if !errors.As(err, &status) || status.Status != event.MessageStatusFail || status.IsCertain || !status.SendNotice || !strings.Contains(status.Message, tc.message) {
			t.Fatalf("%s: err = %v", tc.label, err)
		}
		if len(fake.invites) != 1 || len(h.bot.members) != 0 {
			t.Fatalf("%s: invites=%d member changes=%q", tc.label, len(fake.invites), h.bot.members)
		}
		if len(h.refreshes) != 1 || h.refreshes[0] != testChatID {
			t.Fatalf("%s: roster refreshes = %v", tc.label, h.refreshes)
		}
	}
}

// Inviting a user the fresh source roster already lists sends nothing; the
// roster refresh restores the member in Matrix.
func TestMatrixInviteOfExistingKakaoMemberSendsNothing(t *testing.T) {
	fake := regularGroupFake()
	fake.memberList.MemberIDs = []int64{testSelfID, testOtherID}
	h := newMatrixInviteHarness(t, fake)
	if err := h.invite("$invite:test"); err != nil {
		t.Fatalf("invite of existing member rejected: %v", err)
	}
	if len(fake.invites) != 0 || len(h.bot.members) != 0 {
		t.Fatalf("invites=%+v member changes=%q", fake.invites, h.bot.members)
	}
	if len(h.refreshes) != 1 || h.refreshes[0] != testChatID {
		t.Fatalf("roster refreshes = %v", h.refreshes)
	}
}

// Only plain regular groups use ADDMEM. Open chats, team chats (shared meta
// 15) and 1:1 chats take other official paths, so the invite is refused and
// revoked without reaching KakaoTalk.
func TestMatrixInviteOutsideRegularGroupsIsRejected(t *testing.T) {
	for _, tc := range []struct {
		label string
		data  chatmeta.ChatData
	}{
		{"open chat", chatmeta.ChatData{ChatID: testChatID, Type: "MultiChat", LinkID: 77}},
		{"team chat", chatmeta.ChatData{ChatID: testChatID, Type: "MultiChat", ChatMetas: []chatmeta.ChatMeta{{Type: 15, Content: "{}"}}}},
		{"direct chat", chatmeta.ChatData{ChatID: testChatID, Type: "DirectChat"}},
	} {
		fake := regularGroupFake()
		fake.chatInfo = chatmeta.ChatInfoResponse{ChatData: tc.data}
		h := newMatrixInviteHarness(t, fake)
		err := h.invite("$invite:test")
		var status bridgev2.MessageStatus
		if !errors.As(err, &status) || status.Status != event.MessageStatusFail || !status.IsCertain || !status.SendNotice {
			t.Fatalf("%s: err = %v", tc.label, err)
		}
		if len(fake.invites) != 0 || len(h.refreshes) != 0 {
			t.Fatalf("%s: invites=%+v refreshes=%v", tc.label, fake.invites, h.refreshes)
		}
		if len(h.bot.members) != 1 || h.bot.members[0] != "@kakao_2000:test:leave" {
			t.Fatalf("%s: invite not revoked: %q", tc.label, h.bot.members)
		}
	}
}
