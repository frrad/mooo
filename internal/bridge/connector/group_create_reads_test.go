package connector

import (
	"slices"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

// createdChatRoom is a synthetic CREATE reply chatRoom for group 5000, in the
// field names and types of an observed owned reply (2026-10-15).
func createdChatRoom(t *testing.T, active ...int64) bson.Raw {
	t.Helper()
	ids := bson.A{}
	for _, userID := range active {
		ids = append(ids, userID)
	}
	raw, err := bson.Marshal(bson.D{
		{Key: "chatId", Value: int64(5000)},
		{Key: "members", Value: bson.A{bson.D{{Key: "userId", Value: int64(2000)}, {Key: "nickName", Value: "Two"}}, bson.D{{Key: "userId", Value: int64(4000)}, {Key: "nickName", Value: "Four"}}}},
		{Key: "activeMemberIds", Value: ids},
		{Key: "type", Value: "MultiChat"},
		{Key: "metas", Value: bson.A{}},
		{Key: "ii", Value: bson.A{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func roomReads(backend *countingGroupCreateClient) []string {
	var reads []string
	for _, call := range sourceCalls(backend) {
		if strings.HasPrefix(call, "ChatInfo(") || strings.HasPrefix(call, "MemberList(") || strings.HasPrefix(call, "Members(") {
			reads = append(reads, call)
		}
	}
	return reads
}

// The official client upserts a created room from the CREATE reply's
// chatRoom in full-data mode: its active member IDs and member profiles. The
// bridge binds from the reply with no CHATINFO, MEMLIST or MEMBER.
func TestCreateGroupBindsFromCreateReply(t *testing.T) {
	kc, backend, matrix := newGroupCreationFramework(t)
	backend.createRoom = createdChatRoom(t, 1000, 2000, 4000)
	response, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}})
	if err != nil {
		t.Fatal(err)
	}
	drainMetadataRefresh(t, kc, backend)
	if reads := roomReads(backend); len(reads) != 0 {
		t.Fatalf("binding read the room: %v", reads)
	}
	if len(backend.chatOnRoomCalls) != 0 {
		t.Fatalf("binding sent CHATONROOM: %v", backend.chatOnRoomCalls)
	}
	if response.Portal.MXID != "!selected:test" {
		t.Fatalf("bound room = %q", response.Portal.MXID)
	}
	if got := matrixMembership(matrix, "@kakao_4000:test"); got != event.MembershipJoin {
		t.Fatalf("participant ghost membership = %q, want join", got)
	}
	if roster := kc.verifiedRoster(t.Context(), 5000); !slices.Equal(roster, []int64{1000, 2000, 4000}) {
		t.Fatalf("bound roster = %v", roster)
	}
	ghost, err := kc.login.Bridge.GetGhostByID(t.Context(), makeUserID(4000))
	if err != nil || ghost.Name != "Four" {
		t.Fatalf("reply profile was not applied: %v", err)
	}
}

// A reply without active members cannot supply the roster, so binding reads
// the room as a resumed creation does.
func TestCreateGroupReplyWithoutActiveMembersReadsRoom(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	backend.createRoom = createdChatRoom(t)
	if _, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}); err != nil {
		t.Fatal(err)
	}
	if reads := roomReads(backend); !slices.Equal(reads[:2], []string{"ChatInfo(5000)", "MemberList(5000,0)"}) {
		t.Fatalf("room reads = %v", reads)
	}
}
