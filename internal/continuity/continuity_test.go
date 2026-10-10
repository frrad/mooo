package continuity

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	if err := os.WriteFile(path, []byte(`{"version":7,"clean_shutdown":true,"last_token_id":0,"lbk":0,"chats":[],"known_chats":[],"history_gaps":[],"read_watermarks":[]}`), 0o600); err != nil {
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

func TestOpenMigratesVersionTwoCheckpoint(t *testing.T) {
	path := testPath(t)
	if err := os.WriteFile(path, []byte(`{"version":2,"clean_shutdown":false,"last_token_id":9,"lbk":3,"chats":[{"chat_id":42,"max_log_id":100}],"known_chats":[{"chat_id":42,"max_log_id":105}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := store.Snapshot()
	if got.Version != Version || got.HistoryGaps == nil || len(got.HistoryGaps) != 0 {
		t.Fatalf("migrated checkpoint = %#v", got)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) == "" || !strings.Contains(string(contents), `"version":6`) || !strings.Contains(string(contents), `"history_gaps":[]`) || !strings.Contains(string(contents), `"read_watermarks":[]`) {
		t.Fatalf("migration was not persisted: %s", contents)
	}
}

func TestOpenMigratesVersionThreeCheckpoint(t *testing.T) {
	path := testPath(t)
	if err := os.WriteFile(path, []byte(`{"version":3,"clean_shutdown":false,"last_token_id":9,"lbk":3,"chats":[{"chat_id":42,"max_log_id":100}],"known_chats":[{"chat_id":42,"max_log_id":105}],"history_gaps":[{"chat_id":42,"from_log_id":106,"to_log_id":110}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := store.Snapshot()
	if got.Version != Version || len(got.HistoryGaps) != 1 || got.ReadWatermarks == nil || len(got.ReadWatermarks) != 0 {
		t.Fatalf("migrated checkpoint = %#v", got)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), `"version":6`) || !strings.Contains(string(contents), `"read_watermarks":[]`) {
		t.Fatalf("v3 migration was not persisted: %s", contents)
	}
}

func TestOpenMigratesVersionFourCheckpointDiscardingUnprovenReadWatermarks(t *testing.T) {
	path := testPath(t)
	if err := os.WriteFile(path, []byte(`{"version":4,"clean_shutdown":true,"last_token_id":9,"lbk":3,"chats":[{"chat_id":42,"max_log_id":100}],"known_chats":[{"chat_id":42,"max_log_id":105}],"history_gaps":[],"read_watermarks":[{"chat_id":42,"watermark":105}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := store.Snapshot()
	if got.Version != Version || !store.IsCommitted(42, 100) || len(got.KnownChats) != 1 || got.LastTokenID != 9 {
		t.Fatalf("migrated checkpoint = %#v", got)
	}
	if watermark := store.ReadWatermark(42); watermark != 0 {
		t.Fatalf("migrated read watermark = %d, want discarded", watermark)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), `"version":6`) || !strings.Contains(string(contents), `"read_watermarks":[]`) {
		t.Fatalf("v4 migration was not persisted: %s", contents)
	}
}

func TestReadWatermarksPersistSortedAndMonotonic(t *testing.T) {
	path := testPath(t)
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if advanced, err := store.CommitReadWatermark(9, 100); err != nil || !advanced {
		t.Fatalf("first watermark = (%t, %v)", advanced, err)
	}
	if advanced, err := store.CommitReadWatermark(3, 80); err != nil || !advanced {
		t.Fatalf("second watermark = (%t, %v)", advanced, err)
	}
	if advanced, err := store.CommitReadWatermark(9, 99); err != nil || advanced {
		t.Fatalf("stale watermark = (%t, %v)", advanced, err)
	}
	if got := store.ReadWatermark(9); got != 100 {
		t.Fatalf("watermark = %d, want 100", got)
	}
	if got := store.ReadWatermark(3); got != 80 {
		t.Fatalf("watermark = %d, want 80", got)
	}
	if got := store.ReadWatermark(7); got != 0 {
		t.Fatalf("missing watermark = %d, want 0", got)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := reopened.Snapshot()
	want := []ReadWatermark{{ChatID: 3, Watermark: 80}, {ChatID: 9, Watermark: 100}}
	if !slices.Equal(snapshot.ReadWatermarks, want) {
		t.Fatalf("read watermarks = %#v, want %#v", snapshot.ReadWatermarks, want)
	}
	if got := reopened.ReadWatermark(9); got != 100 {
		t.Fatalf("reopened watermark = %d, want 100", got)
	}
}

func TestReadWatermarkRejectsInvalidInputs(t *testing.T) {
	store, err := Open(testPath(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, values := range [][2]int64{{0, 1}, {-1, 1}, {1, 0}, {1, -1}} {
		if advanced, err := store.CommitReadWatermark(values[0], values[1]); !errors.Is(err, ErrInvalidCursor) || advanced {
			t.Fatalf("invalid watermark %v = (%t, %v)", values, advanced, err)
		}
	}
	if got := store.ReadWatermark(0); got != 0 {
		t.Fatalf("invalid read watermark = %d", got)
	}

	path := testPath(t)
	for _, contents := range []string{
		`{"version":5,"clean_shutdown":true,"last_token_id":0,"lbk":0,"chats":[],"known_chats":[],"history_gaps":[],"read_watermarks":[{"chat_id":0,"watermark":1}]}`,
		`{"version":5,"clean_shutdown":true,"last_token_id":0,"lbk":0,"chats":[],"known_chats":[],"history_gaps":[],"read_watermarks":[{"chat_id":2,"watermark":0}]}`,
		`{"version":5,"clean_shutdown":true,"last_token_id":0,"lbk":0,"chats":[],"known_chats":[],"history_gaps":[],"read_watermarks":[{"chat_id":3,"watermark":2},{"chat_id":3,"watermark":4}]}`,
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(path); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("invalid persisted watermark error = %v", err)
		}
	}
}

func TestReadWatermarkDeletedWithChat(t *testing.T) {
	store, err := Open(testPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitMessage(42, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitReadWatermark(42, 100); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordGap(42, 101, 105); err != nil {
		t.Fatal(err)
	}
	if err := store.InstallSession(nil, nil, nil, []int64{42}, false); err != nil {
		t.Fatal(err)
	}
	if got := store.ReadWatermark(42); got != 0 {
		t.Fatalf("deleted chat watermark = %d, want 0", got)
	}
	if snapshot := store.Snapshot(); len(snapshot.Chats) != 0 || len(snapshot.HistoryGaps) != 0 || len(snapshot.ReadWatermarks) != 0 {
		t.Fatalf("deleted chat state = %#v", snapshot)
	}
}

func TestHistoryGapsPersistMergeResolveAndDelete(t *testing.T) {
	path := testPath(t)
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordGap(9, 101, 105); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordGap(3, 40, 50); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordGap(9, 99, 103); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []HistoryGap{{ChatID: 3, FromLogID: 40, ToLogID: 50}, {ChatID: 9, FromLogID: 99, ToLogID: 105}}
	if got := reopened.Snapshot().HistoryGaps; !slices.Equal(got, want) {
		t.Fatalf("history gaps = %#v, want %#v", got, want)
	}
	if err := reopened.ResolveGapThrough(9, 101); err != nil {
		t.Fatal(err)
	}
	if got := reopened.Snapshot().HistoryGaps[1]; got != (HistoryGap{ChatID: 9, FromLogID: 102, ToLogID: 105}) {
		t.Fatalf("partially resolved gap = %#v", got)
	}
	if err := reopened.InstallSession(nil, nil, nil, []int64{9}, false); err != nil {
		t.Fatal(err)
	}
	if got := reopened.Snapshot().HistoryGaps; len(got) != 1 || got[0].ChatID != 3 {
		t.Fatalf("deleted chat gaps = %#v", got)
	}
	if err := reopened.ResolveGapThrough(3, 50); err != nil {
		t.Fatal(err)
	}
	if got := reopened.Snapshot().HistoryGaps; len(got) != 0 {
		t.Fatalf("resolved gaps = %#v", got)
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
