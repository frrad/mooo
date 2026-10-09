package connector

import (
	"context"
	"errors"
	"testing"

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
	if err = kc.checkSourceAccess(ctx, 5000, p); !errors.Is(err, errMembershipPending) {
		t.Fatalf("pending checkpoint lost after reload: %v", err)
	}
	matrix.members["@kakao_4000:test"] = &event.MemberEventContent{Membership: event.MembershipLeave}
	if err = kc.refreshGroupMembership(ctx, backend, 5000, false); err != nil {
		t.Fatal(err)
	}
	if err = kc.checkSourceAccess(ctx, 5000, p); err != nil {
		t.Fatalf("converged membership still blocked: %v", err)
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
	if err = kc.recordSourceRemoval(ctx, 5000); err != nil {
		t.Fatal(err)
	}
	kc.queue = func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		return bridgev2.EventHandlingResult{Success: true}
	}
	if err = kc.refreshGroupMembership(ctx, backend, 5000, false); err == nil {
		t.Fatal("source roster alone cleared access")
	}
	if err = kc.checkSourceAccess(ctx, 5000, p); err == nil {
		t.Fatal("unjoined Matrix identities allowed forwarding")
	}
	for _, mxid := range []string{"@kakao_1000:test", "@kakao_2000:test", "@kakao_4000:test"} {
		matrix.members[id.UserID(mxid)] = &event.MemberEventContent{Membership: event.MembershipJoin}
	}
	if err = kc.refreshGroupMembership(ctx, backend, 5000, false); err != nil {
		t.Fatal(err)
	}
	if err = kc.checkSourceAccess(ctx, 5000, p); err != nil {
		t.Fatal(err)
	}
	if backend.invites != 0 || backend.creates != 0 {
		t.Fatal("rejoin refresh mutated source")
	}
}

func TestMissingManagedGroupPersistsRemovalBeforeVerifyingMatrixLeave(t *testing.T) {
	ctx := context.Background()
	kc, _, matrix := newGroupCreationFramework(t)
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
	if err = kc.reconcileMissingGroups(ctx, map[int64]struct{}{}); !errors.Is(err, errMembershipPending) {
		t.Fatalf("unapplied leave: %v", err)
	}
	kc.sourceBlocked = nil
	if err = kc.checkSourceAccess(ctx, 5000, p); !errors.Is(err, errSourceAccessRemoved) {
		t.Fatalf("removal lost after reload: %v", err)
	}
	matrix.members[kc.login.UserMXID] = &event.MemberEventContent{Membership: event.MembershipJoin}
	matrix.failMember = kc.login.UserMXID
	matrix.members["@kakao_1000:test"] = &event.MemberEventContent{Membership: event.MembershipLeave}
	if err = kc.reconcileMissingGroups(ctx, map[int64]struct{}{}); !errors.Is(err, errMembershipPending) {
		t.Fatalf("associated Matrix user still joined: %v", err)
	}
	matrix.failMember = ""
	matrix.members[kc.login.UserMXID] = &event.MemberEventContent{Membership: event.MembershipLeave}
	if err = kc.reconcileMissingGroups(ctx, map[int64]struct{}{}); err != nil {
		t.Fatal(err)
	}
	if err = kc.checkSourceAccess(ctx, 5000, p); !errors.Is(err, errSourceAccessRemoved) {
		t.Fatalf("Matrix leave restored source access: %v", err)
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
	if err = kc.checkSourceAccess(ctx, 5000, p); !errors.Is(err, errMembershipPending) {
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
