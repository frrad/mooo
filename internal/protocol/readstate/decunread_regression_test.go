package readstate

import (
	"reflect"
	"testing"
)

func TestDECUNREADMissingRoomPreservesSnapshot(t *testing.T) {
	input := State{CurrentUserID: 7, ActiveMemberCount: 9, ActiveMemberIDs: []int64{7, 8}, BotIDs: map[int64]bool{8: true}, MemberWatermarks: map[int64]int64{8: 12}}
	got := ReduceDECUNREAD(input, Notice{ChatID: 42, UserID: 7, Watermark: 20}, Inputs{})
	if got.Applied {
		t.Fatal("missing room was applied")
	}
	if !reflect.DeepEqual(got.State, input) {
		t.Fatalf("missing room changed snapshot: got %#v want %#v", got.State, input)
	}
}

func TestDECUNREADReturnedSnapshotOwnsCollections(t *testing.T) {
	input := State{
		RoomExists:       true,
		CurrentUserID:    7,
		ActiveMemberIDs:  []int64{7},
		BotIDs:           map[int64]bool{9: true},
		MemberWatermarks: map[int64]int64{7: 10},
	}
	got := ReduceDECUNREAD(input, Notice{ChatID: 42, UserID: 8, Watermark: 12}, Inputs{})
	got.State.ActiveMemberIDs[0] = 99
	got.State.MemberWatermarks[8] = 99
	got.State.BotIDs[9] = false
	if !reflect.DeepEqual(input.ActiveMemberIDs, []int64{7}) {
		t.Fatalf("input active members aliased returned state: %#v", input.ActiveMemberIDs)
	}
	if !reflect.DeepEqual(input.MemberWatermarks, map[int64]int64{7: 10}) {
		t.Fatalf("input watermarks aliased returned state: %#v", input.MemberWatermarks)
	}
	if !reflect.DeepEqual(input.BotIDs, map[int64]bool{9: true}) {
		t.Fatalf("input bot IDs aliased returned state: %#v", input.BotIDs)
	}
}

func TestDECUNREADExistingCountPreservedWithoutMemberAddition(t *testing.T) {
	input := State{RoomExists: true, CurrentUserID: 7, CountOfNewMessage: 2, ActiveMemberIDs: []int64{7}, ActiveMemberCount: 9}
	got := ReduceDECUNREAD(input, Notice{ChatID: 42, UserID: 7, Watermark: 1}, Inputs{})
	if got.State.ActiveMemberCount != 9 {
		t.Fatalf("active count = %d, want preserved 9", got.State.ActiveMemberCount)
	}
}

func TestDECUNREADPositiveCurrentAtLastAlwaysResetsMention(t *testing.T) {
	input := State{RoomExists: true, CurrentUserID: 7, CountOfNewMessage: 2, LastLogID: 100, ActiveMemberIDs: []int64{7}}
	got := ReduceDECUNREAD(input, Notice{ChatID: 42, UserID: 7, Watermark: 100}, Inputs{})
	if got.State.CountOfNewMessage != 0 {
		t.Fatalf("unread count = %d, want 0", got.State.CountOfNewMessage)
	}
	if len(got.Effects) < 2 || got.Effects[1].Kind != "reset_mention_reply" {
		t.Fatalf("effects = %#v, want unconditional mention reset", got.Effects)
	}
}

func TestDECUNREADInactiveMemberEffectOrder(t *testing.T) {
	input := State{RoomExists: true, CurrentUserID: 7, ActiveMemberIDs: []int64{7}, MemberWatermarks: map[int64]int64{}}
	got := ReduceDECUNREAD(input, Notice{ChatID: 42, UserID: 8, Watermark: 12}, Inputs{})
	want := []string{"active_member_add", "member_watermark", "active_member_count", "active_member_projection_refresh", "member_watermark_maintenance"}
	if len(got.Effects) != len(want) {
		t.Fatalf("effects = %#v, want kinds %#v", got.Effects, want)
	}
	for i, effect := range got.Effects {
		if effect.Kind != want[i] {
			t.Fatalf("effect %d = %q, want %q", i, effect.Kind, want[i])
		}
	}
}
