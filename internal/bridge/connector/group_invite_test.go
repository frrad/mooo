package connector

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/frrad/mooo/internal/protocol/chat"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestCompleteGroupInvitationsDurableOnceOnlyThenBind(t *testing.T) {
	for _, outcome := range []string{"accepted", "lost", "warning", "partial"} {
		t.Run(outcome, func(t *testing.T) {
			ctx := context.Background()
			kc, backend, _ := newGroupCreationFramework(t)
			params := &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}
			backend.chatInfo.ChatData.ActiveMemberCount = 2
			backend.memberList.MemberIDs = []int64{1000, 2000}
			if _, err := kc.CreateGroup(ctx, params); err == nil {
				t.Fatal("partial roster bound")
			}
			backend.inviteHook = func(r chat.AddMembersRequest) {
				if r.ChatID != 5000 || !slices.Equal(r.MemberIDs, []int64{4000}) {
					t.Fatalf("invitation selection: %+v", r)
				}
				saved, err := kc.login.Bridge.DB.UserLogin.GetByID(ctx, kc.login.ID)
				if err != nil {
					t.Fatal(err)
				}
				attempt := saved.Metadata.(*UserLoginMetadata).GroupCreates[string(params.RoomID)]
				if !attempt.InviteAttempted || !slices.Equal(attempt.Invitees, r.MemberIDs) {
					t.Fatal("invitation preceded durable reservation")
				}
				if outcome == "accepted" {
					backend.chatInfo.ChatData.ActiveMemberCount = 3
					backend.memberList.MemberIDs = []int64{1000, 2000, 4000}
				}
			}
			if outcome == "lost" {
				backend.inviteErr = errors.New("synthetic lost reply")
			}
			if outcome == "warning" {
				backend.inviteWarning = "synthetic warning"
			}
			result, err := kc.CompleteGroupInvitations(ctx, params.RoomID)
			if outcome == "accepted" {
				if err != nil || result.Portal.MXID != params.RoomID {
					t.Fatalf("accepted invitation: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("incomplete invitation bound")
				}
				// Load the persisted journal to exercise restart suppression.
				saved, loadErr := kc.login.Bridge.DB.UserLogin.GetByID(ctx, kc.login.ID)
				if loadErr != nil {
					t.Fatal(loadErr)
				}
				kc.login.Metadata = saved.Metadata
			}
			if _, err = kc.CompleteGroupInvitations(ctx, params.RoomID); err == nil {
				t.Fatal("completed or uncertain attempt accepted another invitation")
			}
			if backend.invites != 1 || backend.creates != 1 {
				t.Fatalf("source mutations CREATE=%d ADDMEM=%d", backend.creates, backend.invites)
			}
		})
	}
}

func TestCompleteGroupInvitationsRejectsUnsafeSelectionBeforeMutation(t *testing.T) {
	for _, condition := range []string{"unknown_source", "unselected_member", "missing_creator", "invalid_journal", "unselected_matrix_member", "non_regular"} {
		t.Run(condition, func(t *testing.T) {
			ctx := context.Background()
			kc, backend, matrix := newGroupCreationFramework(t)
			room := id.RoomID("!selected:test")
			attempt := groupCreateAttempt{ChatID: 5000, Participants: []int64{2000, 4000}}
			backend.chatInfo.ChatData.ActiveMemberCount = 2
			backend.memberList.MemberIDs = []int64{1000, 2000}
			switch condition {
			case "unknown_source":
				attempt.ChatID = 0
			case "unselected_member":
				backend.memberList.MemberIDs = []int64{1000, 6000}
			case "missing_creator":
				backend.memberList.MemberIDs = []int64{2000, 4000}
			case "invalid_journal":
				attempt.Participants = []int64{2000, 2000}
			case "unselected_matrix_member":
				matrix.members["@stranger:test"] = &event.MemberEventContent{Membership: event.MembershipJoin}
			case "non_regular":
				backend.chatInfo.ChatData.Type = "OpenMultiChat"
			}
			if err := kc.saveGroupAttempt(ctx, room, attempt); err != nil {
				t.Fatal(err)
			}
			if _, err := kc.CompleteGroupInvitations(ctx, room); err == nil {
				t.Fatal("unsafe selection accepted")
			}
			if backend.invites != 0 || backend.creates != 0 {
				t.Fatal("unsafe selection mutated source")
			}
		})
	}
}

func TestCompleteGroupInvitationsSaveFailureCannotMutateSource(t *testing.T) {
	ctx := context.Background()
	kc, backend, _ := newGroupCreationFramework(t)
	params := &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}
	backend.chatInfo.ChatData.ActiveMemberCount = 2
	backend.memberList.MemberIDs = []int64{1000, 2000}
	if _, err := kc.CreateGroup(ctx, params); err == nil {
		t.Fatal("partial roster bound")
	}
	if _, err := kc.login.Bridge.DB.Exec(ctx, `CREATE TRIGGER reject_invitation_journal BEFORE UPDATE ON user_login BEGIN SELECT RAISE(ABORT, 'synthetic journal failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := kc.CompleteGroupInvitations(ctx, params.RoomID); err == nil {
		t.Fatal("failed reservation accepted")
	}
	if backend.invites != 0 {
		t.Fatal("invitation sent after failed reservation")
	}
}

func TestCompleteGroupInvitationsRetainsTemporaryOwnerAfterShutdownFailure(t *testing.T) {
	ctx := context.Background()
	kc, backend, _ := newGroupCreationFramework(t)
	room := id.RoomID("!selected:test")
	if err := kc.saveGroupAttempt(ctx, room, groupCreateAttempt{ChatID: 5000, Participants: []int64{2000, 4000}}); err != nil {
		t.Fatal(err)
	}
	kc.client = nil
	backend.shutdownFailures = 1
	opens := 0
	kc.open = func() (kakaoClient, error) { opens++; return backend, nil }
	if _, err := kc.CompleteGroupInvitations(ctx, room); err == nil {
		t.Fatal("failed profile shutdown reported success")
	}
	if kc.cleanup != backend || opens != 1 || backend.invites != 0 || backend.creates != 0 {
		t.Fatal("temporary owner was lost or a completed roster was mutated")
	}
	if _, err := kc.CompleteGroupInvitations(ctx, room); err == nil {
		t.Fatal("completed attempt reused a failed owner")
	}
	if opens != 1 {
		t.Fatal("opened another owner while cleanup was pending")
	}
	kc.Disconnect()
	if kc.cleanup != nil || backend.closeCalls != 1 {
		t.Fatal("disconnect failed to release retained profile owner")
	}
}
