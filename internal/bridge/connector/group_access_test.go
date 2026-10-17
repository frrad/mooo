package connector

import (
	"context"
	"slices"
	"testing"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestGroupSelfRemovalBlocksOutboundEvenWhenMatrixLeaveFails(t *testing.T) {
	kc, backend, matrix := newGroupCreationFramework(t)
	ctx := context.Background()
	portal, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	portal.MXID = "!selected:test"
	if err = portal.Save(ctx); err != nil {
		t.Fatal(err)
	}
	matrix.members["@kakao_1000:test"] = &event.MemberEventContent{Membership: event.MembershipJoin}
	matrix.failMember = "@kakao_1000:test"
	if kc.handleEvent(backend, events.MemberRemoved{ChatID: 5000, LogID: 9, UserID: kc.userID}) {
		t.Fatal("failed Matrix leave was accepted")
	}
	_, _ = kc.HandleMatrixMessage(ctx, &bridgev2.MatrixMessage{Portal: portal, Content: &event.MessageEventContent{MsgType: event.MsgText, Body: "synthetic after removal"}})
	if len(backend.sends) != 0 {
		t.Fatal("source mutation sent after bridge-account removal")
	}
	// Discard the volatile flag and make the cached object stale: the durable
	// database record must still block sends after reload.
	kc.sourceBlocked = nil
	portal.Metadata = &KakaoPortalMetadata{}
	if _, err = kc.HandleMatrixMessage(ctx, &bridgev2.MatrixMessage{Portal: portal, Content: &event.MessageEventContent{MsgType: event.MsgText, Body: "synthetic after reload"}}); err == nil {
		t.Fatal("durable removal did not block stale-cache send")
	}
	if len(backend.sends) != 0 {
		t.Fatal("source mutation sent after durable reload")
	}
	queued := 0
	kc.queue = func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		queued++
		return bridgev2.EventHandlingResult{Success: true}
	}
	if kc.handleEvent(backend, events.TextMessage{ChatID: 5000, LogID: 10, AuthorID: 2000, Message: "synthetic after removal"}) {
		t.Fatal("removed group content admitted")
	}
	if queued != 0 || len(backend.commits) != 0 {
		t.Fatal("removed group content forwarded or committed")
	}
}

func TestSelfLeaveFrameworkFailureIsNotAcknowledged(t *testing.T) {
	ctx := context.Background()
	kc, backend, matrix := newGroupCreationFramework(t)
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	p.MXID = "!selected:test"
	if err = p.Save(ctx); err != nil {
		t.Fatal(err)
	}
	matrix.members["@kakao_1000:test"] = &event.MemberEventContent{Membership: event.MembershipJoin}
	matrix.failMember = "@kakao_1000:test"
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		//nolint:staticcheck // Drive the real synchronous framework event handler.
		return p.Internal().HandleRemoteEvent(ctx, kc.login, remote.GetType(), remote)
	}
	if kc.handleEvent(backend, events.ChatLeft{ChatID: 5000}) {
		t.Fatal("failed framework leave acknowledged")
	}
	if err = kc.checkSourceAccess(ctx, 5000); err == nil {
		t.Fatal("failed framework leave restored access")
	}
	matrix.failMember = ""
	if !kc.handleEvent(backend, events.ChatLeft{ChatID: 5000}) {
		t.Fatal("successful leave replay not accepted")
	}
	if err = kc.checkSourceAccess(ctx, 5000); err == nil {
		t.Fatal("successful leave restored source access")
	}
}

func TestSelfLeaveOwnerFailureDoesNotRejoinGhost(t *testing.T) {
	ctx := context.Background()
	kc, backend, matrix := newGroupCreationFramework(t)
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	p.MXID = "!selected:test"
	if err = p.Save(ctx); err != nil {
		t.Fatal(err)
	}
	matrix.members["@kakao_1000:test"] = &event.MemberEventContent{Membership: event.MembershipJoin}
	matrix.failMember = "@owner:test"
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		//nolint:staticcheck // Exercise the actual framework membership path.
		return p.Internal().HandleRemoteEvent(ctx, kc.login, remote.GetType(), remote)
	}
	if kc.handleEvent(backend, events.ChatLeft{ChatID: 5000}) {
		t.Fatal("owner removal failure acknowledged")
	}
	if matrix.members["@kakao_1000:test"].Membership != event.MembershipLeave {
		t.Fatal("departed source ghost rejoined while removing owner")
	}
	if err = kc.checkSourceAccess(ctx, 5000); err == nil {
		t.Fatal("owner failure restored source access")
	}
}

func TestSelfLeaveDoesNotUseAdmissionQueue(t *testing.T) {
	ctx := context.Background()
	kc, backend, matrix := newGroupCreationFramework(t)
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	p.MXID = "!selected:test"
	if err = p.Save(ctx); err != nil {
		t.Fatal(err)
	}
	matrix.members["@kakao_1000:test"] = &event.MemberEventContent{Membership: event.MembershipJoin}
	queued := 0
	kc.queue = func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		queued++
		return bridgev2.EventHandlingResult{Success: true}
	}
	if !kc.handleEvent(backend, events.ChatLeft{ChatID: 5000}) {
		t.Fatal("direct leave failed")
	}
	if queued != 0 {
		t.Fatal("departure used queue that invites source user")
	}
	if matrix.members["@kakao_1000:test"].Membership != event.MembershipLeave || matrix.members[kc.login.UserMXID].Membership != event.MembershipLeave {
		t.Fatal("direct leave failed to remove both identities")
	}
}

// roomCreatingQueue stands in for bridgev2 handling the membership resync: an
// event that may create a portal binds a room for a group without one and
// joins the ghosts its chat info lists; any other event for a group without a
// room is dropped, as bridgev2 drops it.
func roomCreatingQueue(t *testing.T, kc *KakaoClient, matrix *groupCreationMatrix, created *[]networkid.PortalID) func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
	return func(evt bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		resync, ok := evt.(*simplevent.ChatResync)
		if !ok {
			return bridgev2.EventHandlingResultSuccess
		}
		portal, err := kc.login.Bridge.GetPortalByKey(t.Context(), resync.PortalKey)
		if err != nil {
			t.Fatal(err)
		}
		if portal.MXID == "" {
			if !resync.ShouldCreatePortal() {
				return bridgev2.EventHandlingResultIgnored
			}
			portal.MXID = id.RoomID("!created-" + string(portal.ID) + ":test")
			if err = portal.Save(t.Context()); err != nil {
				t.Fatal(err)
			}
			*created = append(*created, portal.ID)
		}
		info, err := resync.GetChatInfo(t.Context(), portal)
		if err != nil {
			t.Fatal(err)
		}
		matrix.mu.Lock()
		defer matrix.mu.Unlock()
		matrix.members = map[id.UserID]*event.MemberEventContent{
			"@owner:test": {Membership: event.MembershipJoin},
			"@bot:test":   {Membership: event.MembershipJoin},
		}
		for user := range info.Members.MemberMap {
			matrix.members[id.UserID("@kakao_"+string(user)+":test")] = &event.MemberEventContent{Membership: event.MembershipJoin}
		}
		return bridgev2.EventHandlingResultSuccess
	}
}

// A member leaving a regular group that has no room creates the room from the
// fresh source roster, as login discovery would, rather than leaving the
// group paused with nothing to converge.
func TestMemberRemovalInRoomlessGroupCreatesRoom(t *testing.T) {
	kc, backend, matrix := newGroupCreationFramework(t)
	// After the removal, the source roster no longer lists 4000.
	backend.memberList = chatmeta.MemberListResponse{MemberIDs: []int64{1000, 2000}}
	var created []networkid.PortalID
	kc.queue = roomCreatingQueue(t, kc, matrix, &created)
	var states []status.BridgeState
	kc.sendState = func(state status.BridgeState) { states = append(states, state) }

	if !kc.handleEvent(backend, events.MemberRemoved{ChatID: 5000, LogID: 9, UserID: 4000}) {
		t.Fatal("member removal in a group without a room was rejected")
	}

	if !slices.Equal(created, []networkid.PortalID{"5000"}) {
		t.Fatalf("created rooms = %v, want the group's room", created)
	}
	portal, err := kc.store.portalByKey(t.Context(), makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	if portal == nil || portal.MXID != "!created-5000:test" {
		t.Fatalf("group portal = %+v, want it bound to the created room", portal)
	}
	if err = kc.checkSourceAccess(t.Context(), 5000); err != nil {
		t.Fatalf("group stayed paused after its room was created: %v", err)
	}
	if roster := kc.verifiedRoster(t.Context(), 5000); !slices.Equal(roster, []int64{1000, 2000}) {
		t.Fatalf("verified roster = %v, want the fresh source roster", roster)
	}
	if got := matrixMembership(matrix, "@kakao_4000:test"); got != "" {
		t.Fatalf("departed member's ghost membership = %q, want none", got)
	}
	for _, state := range states {
		if state.Error == stateGroupMembershipPending {
			t.Fatalf("bridge reported %s for a converged group", state.Error)
		}
	}
	if len(backend.commits) != 1 {
		t.Fatalf("commits = %v, want the removal committed", backend.commits)
	}
}

// Before removals could create a room, a removal in a group without a room
// persisted a pause on a portal row with no room and never cleared it. The
// next removal must create the room and clear that pause, so the group's
// messages are delivered again.
func TestMemberRemovalClearsPauseLeftOnRoomlessGroup(t *testing.T) {
	kc, backend, matrix := newGroupCreationFramework(t)
	backend.memberList = chatmeta.MemberListResponse{MemberIDs: []int64{1000, 2000}}
	if _, err := kc.pauseGroupMembership(t.Context(), 5000); err != nil {
		t.Fatal(err)
	}
	// A restart forgets the volatile block; the persisted pause remains.
	kc.sourceBlocked = nil
	if err := kc.checkSourceAccess(t.Context(), 5000); err == nil {
		t.Fatal("setup: the persisted pause did not block the group")
	}
	var created []networkid.PortalID
	kc.queue = roomCreatingQueue(t, kc, matrix, &created)
	kc.sendState = func(status.BridgeState) {}

	if !kc.handleEvent(backend, events.MemberRemoved{ChatID: 5000, LogID: 9, UserID: 4000}) {
		t.Fatal("member removal in a paused group without a room was rejected")
	}

	if !slices.Equal(created, []networkid.PortalID{"5000"}) {
		t.Fatalf("created rooms = %v, want the group's room", created)
	}
	if err := kc.checkSourceAccess(t.Context(), 5000); err != nil {
		t.Fatalf("pause was not cleared: %v", err)
	}
	if !kc.handleEvent(backend, events.TextMessage{ChatID: 5000, LogID: 10, AuthorID: 2000, Message: "synthetic after unpause"}) {
		t.Fatal("message in the unpaused group was rejected")
	}
}

// A removal whose roster refresh fails still leaves the group paused and
// reports the pending state, so forwarding never resumes on a stale roster.
func TestMemberRemovalInRoomlessGroupStaysPausedWhenRoomIsNotCreated(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	backend.memberList = chatmeta.MemberListResponse{MemberIDs: []int64{1000, 2000}}
	kc.queue = func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult { return bridgev2.EventHandlingResultFailed }
	var states []status.BridgeState
	kc.sendState = func(state status.BridgeState) { states = append(states, state) }

	if kc.handleEvent(backend, events.MemberRemoved{ChatID: 5000, LogID: 9, UserID: 4000}) {
		t.Fatal("removal was accepted although the room was not created")
	}

	if err := kc.checkSourceAccess(t.Context(), 5000); err == nil {
		t.Fatal("group was unpaused without a converged room")
	}
	if len(states) == 0 || states[len(states)-1].Error != stateGroupMembershipPending {
		t.Fatalf("states = %+v, want the membership-pending state", states)
	}
	if len(backend.commits) != 0 {
		t.Fatalf("commits = %v, want the removal left uncommitted", backend.commits)
	}
}
