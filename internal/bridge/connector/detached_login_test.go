package connector

import (
	"context"
	"errors"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

// newStoppedUnresolvedCreate prepares a login whose session is down because a
// group creation has an unresolved outcome, the state that blocks bootstrap
// and that the reconciliation commands repair with a detached login.
func newStoppedUnresolvedCreate(t *testing.T) (*KakaoClient, *countingGroupCreateClient, id.RoomID, *int) {
	t.Helper()
	kc, backend, _ := newGroupCreationFramework(t)
	params := &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}
	_, fingerprint, err := kc.groupCreateRequest(params)
	if err != nil {
		t.Fatal(err)
	}
	if err = kc.saveGroupAttempt(context.Background(), params.RoomID, groupCreateAttempt{Fingerprint: fingerprint, Participants: []int64{2000, 4000}}); err != nil {
		t.Fatal(err)
	}
	kc.client = nil
	opens := new(int)
	kc.open = func() (kakaoClient, error) { *opens++; return backend, nil }
	return kc, backend, params.RoomID, opens
}

// Finding 3 of research/bridge/NATIVE-SKEW-AUDIT.md: the detached login
// checked only stopping, connecting and cleanup. After KICKOUT the bridge
// refuses to reconnect, because the same credentials could displace the
// device that caused the kick; an operator command must not log in instead.
func TestReconciliationRefusesDetachedLoginAfterKickout(t *testing.T) {
	kc, backend, room, opens := newStoppedUnresolvedCreate(t)
	kc.login.Metadata.(*UserLoginMetadata).KickedOut = &kickoutRecord{Reason: 1, At: time.Now().UnixMilli()}

	_, err := kc.ReconcileGroup(context.Background(), room, 5000)
	if !errors.Is(err, errDetachedKickedOut) {
		t.Fatalf("ReconcileGroup after KICKOUT = %v, want errDetachedKickedOut", err)
	}
	if _, err = kc.CompleteGroupInvitations(context.Background(), room); err == nil {
		t.Fatal("invitation completion ran after KICKOUT")
	}
	if *opens != 0 || len(backend.calls) != 0 {
		t.Fatalf("detached login after KICKOUT: opens=%d calls=%v", *opens, backend.calls)
	}
}

// A scheduled retry may be waiting out a -328 rate limit; logging in from an
// operator command would skip it.
func TestReconciliationRefusesDetachedLoginWhileRetryIsScheduled(t *testing.T) {
	kc, backend, room, opens := newStoppedUnresolvedCreate(t)
	kc.wait = func(ctx context.Context, _ time.Duration) error { <-ctx.Done(); return ctx.Err() }
	kc.mu.Lock()
	kc.generation = 1
	kc.startRetryLocked(1, rateLimitedRecoveryDelays[0])
	kc.mu.Unlock()
	t.Cleanup(kc.Disconnect)

	_, err := kc.ReconcileGroup(context.Background(), room, 5000)
	if !errors.Is(err, errDetachedRetryPending) {
		t.Fatalf("ReconcileGroup during a scheduled retry = %v, want errDetachedRetryPending", err)
	}
	if *opens != 0 || len(backend.calls) != 0 {
		t.Fatalf("detached login during a scheduled retry: opens=%d calls=%v", *opens, backend.calls)
	}
}

// A restart restores a pending retry from the login's metadata before its
// first Connect; the detached login honors it too.
func TestReconciliationRefusesDetachedLoginBeforePersistedRetry(t *testing.T) {
	kc, backend, room, opens := newStoppedUnresolvedCreate(t)
	kc.login.Metadata.(*UserLoginMetadata).Reconnect = &reconnectRecord{Attempt: 3, NotBefore: time.Now().Add(time.Minute).UnixMilli()}

	_, err := kc.ReconcileGroup(context.Background(), room, 5000)
	if !errors.Is(err, errDetachedRetryPending) {
		t.Fatalf("ReconcileGroup before a persisted retry = %v, want errDetachedRetryPending", err)
	}
	if *opens != 0 || len(backend.calls) != 0 {
		t.Fatalf("detached login before a persisted retry: opens=%d calls=%v", *opens, backend.calls)
	}
}

// When no gate applies the detached login proceeds, because the unresolved
// creation blocks every bootstrap, and it is recorded in the persisted
// schedule like any other login.
func TestReconciliationDetachedLoginIsRecordedInSchedule(t *testing.T) {
	kc, backend, room, opens := newStoppedUnresolvedCreate(t)
	before := time.Now().UnixMilli()

	if _, err := kc.ReconcileGroup(context.Background(), room, 5000); err != nil {
		t.Fatal(err)
	}
	record := kc.login.Metadata.(*UserLoginMetadata).Reconnect
	if *opens != 1 || backend.closeCalls != 1 {
		t.Fatalf("opens=%d closes=%d, want one released detached owner", *opens, backend.closeCalls)
	}
	if record == nil || record.LoginAt < before || record.NotBefore != 0 {
		t.Fatalf("detached login was not recorded in the reconnect schedule: %+v", record)
	}
}
