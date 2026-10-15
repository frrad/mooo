package connector

import (
	"slices"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
)

// createdChatRoom is a synthetic CREATE reply chatRoom for group 5000.
func createdChatRoom(t *testing.T) bson.Raw {
	t.Helper()
	raw, err := bson.Marshal(bson.D{{Key: "chatId", Value: int64(5000)}, {Key: "type", Value: "MultiChat"}, {Key: "activeMembersCount", Value: int32(3)}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func roomReads(backend *countingGroupCreateClient) []string {
	var reads []string
	for _, call := range sourceCalls(backend) {
		if strings.HasPrefix(call, "ChatInfo(") || strings.HasPrefix(call, "MemberList(") {
			reads = append(reads, call)
		}
	}
	return reads
}

// The official client upserts the room from the CREATE reply's chatRoom and
// opens it, which sends CHATONROOM; a full CHATONROOM lists every active
// member. The bridge binds from those two replies with no CHATINFO or MEMLIST.
func TestCreateGroupBindsFromCreateReplyAndChatOnRoom(t *testing.T) {
	kc, backend, matrix := newGroupCreationFramework(t)
	backend.createRoom = createdChatRoom(t)
	backend.chatOnRoom = map[int64]chatmeta.ChatOnRoomResponse{5000: {ChatID: 5000, Full: true, Watermarks: map[int64]int64{1000: 0, 2000: 0, 4000: 0}}}
	response, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}})
	if err != nil {
		t.Fatal(err)
	}
	if reads := roomReads(backend); len(reads) != 0 {
		t.Fatalf("binding read the room: %v", reads)
	}
	if !slices.Equal(backend.chatOnRoomCalls, []int64{5000}) {
		t.Fatalf("CHATONROOM calls = %v, want one", backend.chatOnRoomCalls)
	}
	if response.Portal.MXID != "!selected:test" {
		t.Fatalf("bound room = %q", response.Portal.MXID)
	}
	if got := matrixMembership(matrix, "@kakao_4000:test"); got != event.MembershipJoin {
		t.Fatalf("participant ghost membership = %q, want join", got)
	}
	roster := kc.verifiedRoster(t.Context(), 5000)
	slices.Sort(roster)
	if !slices.Equal(roster, []int64{1000, 2000, 4000}) {
		t.Fatalf("bound roster = %v", roster)
	}
}

// A CHATONROOM that is not a full member list cannot serve as the roster.
func TestCreateGroupWithPartialChatOnRoomReadsRoster(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	backend.createRoom = createdChatRoom(t)
	backend.chatOnRoom = map[int64]chatmeta.ChatOnRoomResponse{5000: {ChatID: 5000, Watermarks: map[int64]int64{1000: 0}}}
	if _, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}); err != nil {
		t.Fatal(err)
	}
	if reads := roomReads(backend); !slices.Equal(reads, []string{"MemberList(5000,0)"}) {
		t.Fatalf("room reads = %v, want MEMLIST only", reads)
	}
}

// Without a usable chatRoom (here: none) the bridge reads CHATINFO, as the
// resume of an interrupted creation does.
func TestCreateGroupWithoutChatRoomReadsChatInfo(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	backend.chatOnRoom = map[int64]chatmeta.ChatOnRoomResponse{5000: {ChatID: 5000, Full: true, Watermarks: map[int64]int64{1000: 0, 2000: 0, 4000: 0}}}
	if _, err := kc.CreateGroup(t.Context(), &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}); err != nil {
		t.Fatal(err)
	}
	if reads := roomReads(backend); !slices.Equal(reads, []string{"ChatInfo(5000)", "MemberList(5000,0)"}) {
		t.Fatalf("room reads = %v", reads)
	}
}
