package connector

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestMembershipCheckpointKeepsForwardingPausedUntilDepartedGhostLeaves(t *testing.T) {
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
	backend.chatInfo.ChatData.ActiveMemberCount = 2
	backend.memberList.MemberIDs = []int64{1000, 2000}
	for _, mxid := range []string{"@kakao_1000:test", "@kakao_2000:test", "@kakao_4000:test"} {
		matrix.members[id.UserID(mxid)] = &event.MemberEventContent{Membership: event.MembershipJoin}
	}
	// Framework success cannot prove that a failed Matrix leave was applied.
	kc.queue = func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		return bridgev2.EventHandlingResult{Success: true}
	}
	if err = kc.refreshGroupMembership(ctx, backend, 5000, false); !errors.Is(err, errMembershipPending) {
		t.Fatalf("unapplied leave: %v", err)
	}
	kc.sourceBlocked = nil
	if err = kc.checkSourceAccess(ctx, 5000); !errors.Is(err, errMembershipPending) {
		t.Fatalf("pending checkpoint lost after reload: %v", err)
	}
	matrix.members["@kakao_4000:test"] = &event.MemberEventContent{Membership: event.MembershipLeave}
	if err = kc.refreshGroupMembership(ctx, backend, 5000, false); err != nil {
		t.Fatal(err)
	}
	if err = kc.checkSourceAccess(ctx, 5000); err != nil {
		t.Fatalf("converged membership still blocked: %v", err)
	}
}

func TestMembershipAcceptsDoublePuppetForSelfGhost(t *testing.T) {
	ctx := context.Background()
	kc, backend, matrix := newGroupCreationFramework(t)
	matrix.doublePuppet = true
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	p.MXID = "!selected:test"
	if err = p.Save(ctx); err != nil {
		t.Fatal(err)
	}
	backend.chatInfo.ChatData.ActiveMemberCount = 2
	backend.memberList.MemberIDs = []int64{1000, 2000}
	// With double puppeting, bridgev2 joins the owner's real account instead
	// of the self ghost, so @kakao_1000 is never in the room.
	matrix.members["@owner:test"] = &event.MemberEventContent{Membership: event.MembershipJoin}
	matrix.members["@kakao_2000:test"] = &event.MemberEventContent{Membership: event.MembershipJoin}
	matrix.members["@kakao_4000:test"] = &event.MemberEventContent{Membership: event.MembershipLeave}
	kc.queue = func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		return bridgev2.EventHandlingResult{Success: true}
	}
	if err = kc.refreshGroupMembership(ctx, backend, 5000, false); err != nil {
		t.Fatalf("double-puppeted owner should satisfy the self member: %v", err)
	}
}

func TestMembershipWithoutDoublePuppetStillNeedsSelfGhost(t *testing.T) {
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
	backend.chatInfo.ChatData.ActiveMemberCount = 2
	backend.memberList.MemberIDs = []int64{1000, 2000}
	// Without double puppeting, bridgev2 sends as the self ghost. The owner's
	// real account being joined does not put that ghost in the room.
	matrix.members["@owner:test"] = &event.MemberEventContent{Membership: event.MembershipJoin}
	matrix.members["@kakao_2000:test"] = &event.MemberEventContent{Membership: event.MembershipJoin}
	matrix.members["@kakao_4000:test"] = &event.MemberEventContent{Membership: event.MembershipLeave}
	kc.queue = func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		return bridgev2.EventHandlingResult{Success: true}
	}
	if err = kc.refreshGroupMembership(ctx, backend, 5000, false); !errors.Is(err, errMembershipPending) {
		t.Fatalf("self ghost absent: %v", err)
	}
	matrix.members["@kakao_1000:test"] = &event.MemberEventContent{Membership: event.MembershipJoin}
	if err = kc.refreshGroupMembership(ctx, backend, 5000, false); err != nil {
		t.Fatal(err)
	}
}

func TestSourceRejoinClearsRemovalOnlyWithFreshRosterAndMatrixConvergence(t *testing.T) {
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
	if err = kc.recordSourceRemoval(ctx, 5000, "test"); err != nil {
		t.Fatal(err)
	}
	kc.queue = func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		return bridgev2.EventHandlingResult{Success: true}
	}
	if err = kc.refreshGroupMembership(ctx, backend, 5000, false); err == nil {
		t.Fatal("source roster alone cleared access")
	}
	if err = kc.checkSourceAccess(ctx, 5000); err == nil {
		t.Fatal("unjoined Matrix identities allowed forwarding")
	}
	for _, mxid := range []string{"@kakao_1000:test", "@kakao_2000:test", "@kakao_4000:test"} {
		matrix.members[id.UserID(mxid)] = &event.MemberEventContent{Membership: event.MembershipJoin}
	}
	if err = kc.refreshGroupMembership(ctx, backend, 5000, false); err != nil {
		t.Fatal(err)
	}
	if err = kc.checkSourceAccess(ctx, 5000); err != nil {
		t.Fatal(err)
	}
	if backend.invites != 0 || backend.creates != 0 {
		t.Fatal("rejoin refresh mutated source")
	}
}

func TestMissingManagedGroupPersistsRemovalBeforeVerifyingMatrixLeave(t *testing.T) {
	ctx := context.Background()
	kc, backend, matrix := newGroupCreationFramework(t)
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	p.MXID = "!selected:test"
	p.Metadata = &KakaoPortalMetadata{GroupMembershipManaged: true}
	if err = p.Save(ctx); err != nil {
		t.Fatal(err)
	}
	matrix.members["@kakao_1000:test"] = &event.MemberEventContent{Membership: event.MembershipJoin}
	matrix.failMember = "@kakao_1000:test"
	if err = kc.reconcileMissingGroups(ctx, backend, map[int64]struct{}{}, nil, map[int64]struct{}{5000: {}}); !errors.Is(err, errMembershipPending) {
		t.Fatalf("unapplied leave: %v", err)
	}
	kc.sourceBlocked = nil
	if err = kc.checkSourceAccess(ctx, 5000); !errors.Is(err, errSourceAccessRemoved) {
		t.Fatalf("removal lost after reload: %v", err)
	}
	matrix.members[kc.login.UserMXID] = &event.MemberEventContent{Membership: event.MembershipJoin}
	matrix.failMember = kc.login.UserMXID
	matrix.members["@kakao_1000:test"] = &event.MemberEventContent{Membership: event.MembershipLeave}
	if err = kc.reconcileMissingGroups(ctx, backend, map[int64]struct{}{}, nil, nil); !errors.Is(err, errMembershipPending) {
		t.Fatalf("associated Matrix user still joined: %v", err)
	}
	matrix.failMember = ""
	matrix.members[kc.login.UserMXID] = &event.MemberEventContent{Membership: event.MembershipLeave}
	if err = kc.reconcileMissingGroups(ctx, backend, map[int64]struct{}{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err = kc.checkSourceAccess(ctx, 5000); !errors.Is(err, errSourceAccessRemoved) {
		t.Fatalf("Matrix leave restored source access: %v", err)
	}
	// The deletion evidence arrives once; the persisted reason alone drives the
	// Matrix leave retries, and a departed account reads no source metadata.
	if len(backend.metadataCalls) != 0 {
		t.Fatalf("departed group read source metadata: %v", backend.metadataCalls)
	}
}

func TestPeerRemovalFrameworkFailureKeepsCheckpointPending(t *testing.T) {
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
	backend.chatInfo.ChatData.ActiveMemberCount = 2
	backend.memberList.MemberIDs = []int64{1000, 2000}
	for _, mxid := range []id.UserID{"@kakao_1000:test", "@kakao_2000:test", "@kakao_4000:test"} {
		matrix.members[mxid] = &event.MemberEventContent{Membership: event.MembershipJoin}
	}
	matrix.failMember = "@kakao_4000:test"
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		//nolint:staticcheck // Exercise framework full-roster synchronization.
		return p.Internal().HandleRemoteEvent(ctx, kc.login, remote.GetType(), remote)
	}
	if err = kc.refreshGroupMembership(ctx, backend, 5000, false); !errors.Is(err, errMembershipPending) {
		t.Fatalf("failed peer leave: %v", err)
	}
	kc.sourceBlocked = nil
	if err = kc.checkSourceAccess(ctx, 5000); !errors.Is(err, errMembershipPending) {
		t.Fatalf("checkpoint lost: %v", err)
	}
	matrix.failMember = ""
	if err = kc.refreshGroupMembership(ctx, backend, 5000, false); err != nil {
		t.Fatal(err)
	}
	if member := matrix.members["@kakao_4000:test"]; member == nil || member.Membership != event.MembershipLeave {
		t.Fatal("departed ghost still joined")
	}
}

// The bridge state for a source removal does not name the chat, so the log
// line is the operator's only way to find which chat was blocked.
func TestSourceRemovalLogsWhichChat(t *testing.T) {
	ctx := context.Background()
	kc, _, _ := newGroupCreationFramework(t)
	var logs bytes.Buffer
	kc.login.Log = zerolog.New(&logs)
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	p.MXID = "!selected:test"
	if err = p.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if err = kc.recordSourceRemoval(ctx, 5000, "missing_from_chat_inventory"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"level":"warn"`, `"kakao_chat_id":5000`, `"portal_mxid":"!selected:test"`, `"source_removal_reason":"missing_from_chat_inventory"`} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("log %q lacks %s", logs.String(), want)
		}
	}
}
