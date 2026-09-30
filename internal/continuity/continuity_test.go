package continuity

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func testPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "profile.continuity")
}

func TestStorePersistsSortedResumeBoundary(t *testing.T) {
	path := testPath(t)
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := mustStat(t, path).Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %o", mode)
	}
	lastTokenID := int64(41)
	lbk := int32(7)
	if err := store.InstallSession(&lastTokenID, &lbk, nil, nil, true); err != nil {
		t.Fatal(err)
	}
	if advanced, err := store.CommitMessage(9, 100); err != nil || !advanced {
		t.Fatalf("first commit = (%t, %v)", advanced, err)
	}
	if advanced, err := store.CommitMessage(3, 80); err != nil || !advanced {
		t.Fatalf("second commit = (%t, %v)", advanced, err)
	}
	if advanced, err := store.CommitMessage(9, 99); err != nil || advanced {
		t.Fatalf("stale commit = (%t, %v)", advanced, err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := reopened.Snapshot()
	if snapshot.CleanShutdown || snapshot.LastTokenID != 41 || snapshot.LBK != 7 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	chatIDs, maxIDs := snapshot.LoginCursors()
	if len(chatIDs) != 2 || chatIDs[0] != 3 || maxIDs[0] != 80 || chatIDs[1] != 9 || maxIDs[1] != 100 {
		t.Fatalf("login cursors = %v / %v", chatIDs, maxIDs)
	}
	if !reopened.IsCommitted(9, 100) || !reopened.IsCommitted(9, 50) || reopened.IsCommitted(9, 101) {
		t.Fatal("committed boundary mismatch")
	}
	if err := reopened.MarkClean(); err != nil {
		t.Fatal(err)
	}
	if !reopened.Snapshot().CleanShutdown {
		t.Fatal("checkpoint remained interrupted")
	}
}

func TestStoreRejectsUnknownVersionAndUnsafeMode(t *testing.T) {
	path := testPath(t)
	if err := os.WriteFile(path, []byte(`{"version":3,"clean_shutdown":true,"last_token_id":0,"lbk":0,"chats":[],"known_chats":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("version error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":2,"clean_shutdown":true,"last_token_id":0,"lbk":0,"chats":[],"known_chats":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrUnsafePermissions) {
		t.Fatalf("permissions error = %v", err)
	}
}

func TestInstallSessionRejectsCursorRegressionInputs(t *testing.T) {
	store, err := Open(testPath(t))
	if err != nil {
		t.Fatal(err)
	}
	negative := int64(-1)
	if err := store.InstallSession(&negative, nil, nil, nil, false); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("negative cursor error = %v", err)
	}
	if _, err := store.CommitMessage(0, 1); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("invalid message error = %v", err)
	}
}

func TestDeltaLoginPreservesKnownChatWithoutAcknowledgingIt(t *testing.T) {
	path := testPath(t)
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	firstToken := int64(41)
	if err := store.InstallSession(&firstToken, nil, []ChatTarget{{ChatID: 42, MaxLogID: 105}}, nil, true); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().SyncTargets(); len(got) != 1 || got[0] != (ChatTarget{ChatID: 42, MaxLogID: 105}) {
		t.Fatalf("full-login targets = %#v", got)
	}
	if chatIDs, maxIDs := store.Snapshot().LoginCursors(); len(chatIDs) != 0 || len(maxIDs) != 0 {
		t.Fatalf("uncommitted target leaked into LOGINLIST = %v / %v", chatIDs, maxIDs)
	}

	// Reopening models the real failure boundary: a new process receives a
	// delta login with no chatDatas and must not forget the full inventory.
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	secondToken := int64(42)
	if err := reopened.InstallSession(&secondToken, nil, nil, nil, false); err != nil {
		t.Fatal(err)
	}
	if got := reopened.Snapshot().SyncTargets(); len(got) != 1 || got[0] != (ChatTarget{ChatID: 42, MaxLogID: 105}) {
		t.Fatalf("delta-login targets = %#v", got)
	}
	if chatIDs, _ := reopened.Snapshot().LoginCursors(); len(chatIDs) != 0 {
		t.Fatalf("delta login acknowledged uncommitted target: %v", chatIDs)
	}

	if advanced, err := reopened.CommitMessage(42, 101); err != nil || !advanced {
		t.Fatalf("partial commit = (%t, %v)", advanced, err)
	}
	if got := reopened.Snapshot().SyncTargets(); len(got) != 1 || got[0].MaxLogID != 105 {
		t.Fatalf("partial commit lowered recovery target: %#v", got)
	}
	if advanced, err := reopened.CommitMessage(42, 105); err != nil || !advanced {
		t.Fatalf("final commit = (%t, %v)", advanced, err)
	}
	chatIDs, maxIDs := reopened.Snapshot().LoginCursors()
	if len(chatIDs) != 1 || chatIDs[0] != 42 || maxIDs[0] != 105 {
		t.Fatalf("committed LOGINLIST cursors = %v / %v", chatIDs, maxIDs)
	}
	thirdToken := int64(43)
	if err := reopened.InstallSession(&thirdToken, nil, nil, []int64{42}, false); err != nil {
		t.Fatal(err)
	}
	if got := reopened.Snapshot().SyncTargets(); len(got) != 0 {
		t.Fatalf("deleted chat remains in inventory: %#v", got)
	}
	if chatIDs, _ := reopened.Snapshot().LoginCursors(); len(chatIDs) != 0 {
		t.Fatalf("deleted chat remains committed: %v", chatIDs)
	}
}

func TestInstallSessionAppliesDeletionBeforeObservedDelta(t *testing.T) {
	store, err := Open(testPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitMessage(42, 100); err != nil {
		t.Fatal(err)
	}
	token := int64(8)
	if err := store.InstallSession(
		&token,
		nil,
		[]ChatTarget{{ChatID: 42, MaxLogID: 105}},
		[]int64{42},
		false,
	); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().SyncTargets(); len(got) != 1 || got[0] != (ChatTarget{ChatID: 42, MaxLogID: 105}) {
		t.Fatalf("recreated target = %#v", got)
	}
	if chatIDs, maxIDs := store.Snapshot().LoginCursors(); len(chatIDs) != 0 || len(maxIDs) != 0 {
		t.Fatalf("deleted commit survived recreated delta = %v / %v", chatIDs, maxIDs)
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
