package continuity

import (
	"os"
	"testing"
)

func TestVersionFiveMigrationPreservesCommittedAndReadState(t *testing.T) {
	path := testPath(t)
	data := `{"version":5,"clean_shutdown":false,"last_token_id":9,"lbk":3,"chats":[{"chat_id":42,"max_log_id":100}],"known_chats":[{"chat_id":42,"max_log_id":105}],"history_gaps":[],"read_watermarks":[{"chat_id":42,"watermark":99}]}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	if state.Version != Version || !store.IsCommitted(42, 100) || store.ReadWatermark(42) != 99 || state.LastTokenID != 9 || state.LBK != 3 || len(state.DeliveryStarts) != 0 {
		t.Fatal("migration changed existing continuity")
	}
	reopened, err := Open(path)
	if err != nil || reopened.Snapshot().Version != Version {
		t.Fatal("migration was not persisted")
	}
}

func TestDeliveryStartRetainsEarliestAdmissionAndDoesNotAcknowledge(t *testing.T) {
	path := testPath(t)
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, logID := range []int64{103, 101, 105} {
		if err := store.RecordDeliveryStart(42, logID); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	state := reopened.Snapshot()
	if len(state.DeliveryStarts) != 1 || state.DeliveryStarts[0].FirstLogID != 101 || state.KnownChats[0].MaxLogID != 105 {
		t.Fatal("admission boundary lost earliest or latest observed position")
	}
	ids, _ := state.LoginCursors()
	if len(ids) != 0 || reopened.ReadWatermark(42) != 0 {
		t.Fatal("admission acknowledged messages")
	}
	clone := state.Clone()
	clone.DeliveryStarts[0].FirstLogID = 999
	if reopened.Snapshot().DeliveryStarts[0].FirstLogID != 101 {
		t.Fatal("snapshot alias")
	}
	if _, err := reopened.CommitMessage(42, 101); err != nil {
		t.Fatal(err)
	}
	if len(reopened.Snapshot().DeliveryStarts) != 0 {
		t.Fatal("commit retained obsolete admission floor")
	}
	if err := reopened.RecordDeliveryStart(42, 110); err != nil {
		t.Fatal(err)
	}
	if len(reopened.Snapshot().DeliveryStarts) != 0 {
		t.Fatal("committed room requires no admission floor")
	}
}

func TestDeletedSourceChatCannotRetainReplayAuthorization(t *testing.T) {
	path := testPath(t)
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordDeliveryStart(42, 101); err != nil {
		t.Fatal(err)
	}
	if err := store.InstallSession(nil, nil, nil, []int64{42}, false); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Snapshot().DeliveryStarts) != 0 || len(reopened.Snapshot().KnownChats) != 0 {
		t.Fatal("deleted source room retained replay authority")
	}
}
