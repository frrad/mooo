package connector

import (
	"context"
	"testing"

	"github.com/frrad/mooo/internal/protocol/events"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
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
	if err = kc.checkSourceAccess(ctx, 5000, p); err == nil {
		t.Fatal("failed framework leave restored access")
	}
	matrix.failMember = ""
	if !kc.handleEvent(backend, events.ChatLeft{ChatID: 5000}) {
		t.Fatal("successful leave replay not accepted")
	}
	if err = kc.checkSourceAccess(ctx, 5000, p); err == nil {
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
	if err = kc.checkSourceAccess(ctx, 5000, p); err == nil {
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
