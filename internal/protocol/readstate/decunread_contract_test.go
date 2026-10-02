package readstate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type decUnreadFixture struct {
	Name   string              `json:"name"`
	State  decUnreadStateJSON  `json:"state"`
	Notice decUnreadNoticeJSON `json:"notice"`
	Inputs decUnreadInputsJSON `json:"inputs"`
	Want   decUnreadWantJSON   `json:"want"`
}

type decUnreadStateJSON struct {
	RoomExists          bool            `json:"roomExists"`
	CurrentUserID       int64           `json:"currentUserId"`
	CountOfNewMessage   int64           `json:"countOfNewMessage"`
	LastLogID           int64           `json:"lastLogId"`
	LastSeenLogID       int64           `json:"lastSeenLogId"`
	MentionReplyPresent bool            `json:"mentionReplyPresent"`
	MemberWatermarks    map[int64]int64 `json:"memberWatermarks"`
	ActiveMemberIDs     []int64         `json:"activeMemberIds"`
	ActiveMemberCount   *int            `json:"activeMemberCount"`
	BotIDs              map[int64]bool  `json:"botIds"`
	RoomType            int32           `json:"roomType"`
	Frozen              bool            `json:"frozen"`
}

type decUnreadNoticeJSON struct {
	ChatID    int64 `json:"chatId"`
	UserID    int64 `json:"userId"`
	Watermark int64 `json:"watermark"`
}

type decUnreadInputsJSON struct {
	EligibleUnreadCount *int64 `json:"eligibleUnreadCount"`
}

type decUnreadWantJSON struct {
	Applied             bool                  `json:"applied"`
	UnreadCount         *int64                `json:"unreadCount"`
	MentionReplyPresent bool                  `json:"mentionReplyPresent"`
	MemberWatermarks    map[int64]int64       `json:"memberWatermarks"`
	ActiveMemberIDs     []int64               `json:"activeMemberIds"`
	ActiveMemberCount   *int                  `json:"activeMemberCount"`
	Effects             []decUnreadEffectJSON `json:"effects"`
}

type decUnreadEffectJSON struct {
	Kind           string  `json:"kind"`
	ChatID         int64   `json:"chatId,omitempty"`
	UserID         int64   `json:"userId,omitempty"`
	Watermark      int64   `json:"watermark,omitempty"`
	Count          int64   `json:"count,omitempty"`
	LowerBound     int64   `json:"lowerBound,omitempty"`
	ExcludedType   int32   `json:"excludedType,omitempty"`
	ExcludedStatus int32   `json:"excludedStatus,omitempty"`
	AllowedScopes  []int32 `json:"allowedScopes,omitempty"`
}

func TestDECUNREADContractVectors(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("testdata", "decunread_contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []decUnreadFixture
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			inputs := Inputs{}
			if fixture.Inputs.EligibleUnreadCount != nil {
				inputs.EligibleUnreadCount = *fixture.Inputs.EligibleUnreadCount
			}
			state := State{
				RoomExists: fixture.State.RoomExists, CurrentUserID: fixture.State.CurrentUserID,
				CountOfNewMessage: fixture.State.CountOfNewMessage, LastLogID: fixture.State.LastLogID,
				LastSeenLogID: fixture.State.LastSeenLogID, MentionReplyPresent: fixture.State.MentionReplyPresent,
				MemberWatermarks: fixture.State.MemberWatermarks, ActiveMemberIDs: fixture.State.ActiveMemberIDs,
				BotIDs: fixture.State.BotIDs, RoomType: fixture.State.RoomType, Frozen: fixture.State.Frozen,
			}
			beforeMembers := cloneWatermarks(state.MemberWatermarks)
			beforeActive := append([]int64(nil), state.ActiveMemberIDs...)
			beforeBots := cloneBots(state.BotIDs)
			got := ReduceDECUNREAD(state, Notice{ChatID: fixture.Notice.ChatID, UserID: fixture.Notice.UserID, Watermark: fixture.Notice.Watermark}, inputs)
			if !reflect.DeepEqual(state.MemberWatermarks, beforeMembers) || !reflect.DeepEqual(state.ActiveMemberIDs, beforeActive) || !reflect.DeepEqual(state.BotIDs, beforeBots) {
				t.Fatalf("input member watermarks mutated: got %#v, want %#v", state.MemberWatermarks, beforeMembers)
			}
			if got.Applied != fixture.Want.Applied || got.State.UnreadCount != expectedUnread(fixture.Want.UnreadCount, fixture.State.CountOfNewMessage) ||
				got.State.MentionReplyPresent != fixture.Want.MentionReplyPresent ||
				got.State.ActiveMemberCount != wantInt(fixture.Want.ActiveMemberCount) {
				t.Fatalf("state result = %#v, want applied=%v unread=%v mention-present=%v", got, fixture.Want.Applied, fixture.Want.UnreadCount, fixture.Want.MentionReplyPresent)
			}
			if !reflect.DeepEqual(got.State.MemberWatermarks, fixture.Want.MemberWatermarks) || !reflect.DeepEqual(got.State.ActiveMemberIDs, fixture.Want.ActiveMemberIDs) {
				t.Fatalf("member state = %#v/%#v, want %#v/%#v", got.State.MemberWatermarks, got.State.ActiveMemberIDs, fixture.Want.MemberWatermarks, fixture.Want.ActiveMemberIDs)
			}
			encoded, err := json.Marshal(got.Effects)
			if err != nil {
				t.Fatal(err)
			}
			var effects []decUnreadEffectJSON
			effectDecoder := json.NewDecoder(bytes.NewReader(encoded))
			effectDecoder.DisallowUnknownFields()
			if err := effectDecoder.Decode(&effects); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(effects, fixture.Want.Effects) {
				t.Fatalf("effects = %#v, want %#v", effects, fixture.Want.Effects)
			}
		})
	}
}

func TestDECUNREADContractJSONRejectsUnknownFields(t *testing.T) {
	decoder := json.NewDecoder(bytes.NewBufferString(`[{"name":"unknown","unexpected":true}]`))
	decoder.DisallowUnknownFields()
	var fixtures []decUnreadFixture
	if err := decoder.Decode(&fixtures); err == nil {
		t.Fatal("unknown fixture field was accepted")
	}
}

func cloneWatermarks(input map[int64]int64) map[int64]int64 {
	if input == nil {
		return nil
	}
	output := make(map[int64]int64, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func wantInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func expectedUnread(value *int64, initial int64) int64 {
	if value == nil {
		return initial
	}
	return *value
}

func cloneBots(input map[int64]bool) map[int64]bool {
	if input == nil {
		return nil
	}
	output := make(map[int64]bool, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
