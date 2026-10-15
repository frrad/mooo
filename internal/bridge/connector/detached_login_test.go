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

// A detached login while a recovery episode runs would add a login the
// native client never sends.
func TestReconciliationRefusesDetachedLoginWhileRetryIsScheduled(t *testing.T) {
	kc, backend, room, opens := newStoppedUnresolvedCreate(t)
	kc.wait = func(ctx context.Context, _ time.Duration) error { <-ctx.Done(); return ctx.Err() }
	kc.mu.Lock()
	kc.generation = 1
	kc.startRetryLocked(1, episodeRetryDelay)
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

func TestReconciliationRefusesDetachedLoginDuringEpisode(t *testing.T) {
	kc, backend, room, opens := newStoppedUnresolvedCreate(t)
	kc.mu.Lock()
	kc.schedule.begin(time.Now())
	kc.mu.Unlock()

	_, err := kc.ReconcileGroup(context.Background(), room, 5000)
	if !errors.Is(err, errDetachedRetryPending) {
		t.Fatalf("ReconcileGroup during an episode = %v, want errDetachedRetryPending", err)
	}
	if *opens != 0 || len(backend.calls) != 0 {
		t.Fatalf("detached login during an episode: opens=%d calls=%v", *opens, backend.calls)
	}
}

// After the episode watchdog stopped recovery, only the operator's reconnect
// command may log in again.
func TestReconciliationRefusesDetachedLoginAfterRecoveryStopped(t *testing.T) {
	kc, backend, room, opens := newStoppedUnresolvedCreate(t)
	kc.mu.Lock()
	kc.schedule = recoverySchedule{gaveUp: true}
	kc.mu.Unlock()

	_, err := kc.ReconcileGroup(context.Background(), room, 5000)
	if !errors.Is(err, errDetachedRecoveryStopped) {
		t.Fatalf("ReconcileGroup after recovery stopped = %v, want errDetachedRecoveryStopped", err)
	}
	if *opens != 0 || len(backend.calls) != 0 {
		t.Fatalf("detached login after recovery stopped: opens=%d calls=%v", *opens, backend.calls)
	}
}

// When no gate applies the detached login proceeds, because the unresolved
// creation blocks every bootstrap.
func TestReconciliationDetachedLoginProceedsWithoutGate(t *testing.T) {
	kc, backend, room, opens := newStoppedUnresolvedCreate(t)

	if _, err := kc.ReconcileGroup(context.Background(), room, 5000); err != nil {
		t.Fatal(err)
	}
	if *opens != 1 || backend.closeCalls != 1 {
		t.Fatalf("opens=%d closes=%d, want one released detached owner", *opens, backend.closeCalls)
	}
}
