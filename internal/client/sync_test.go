package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestCatchUpPagesWithoutAdvancingCheckpoint(t *testing.T) {
	checkpoint := testCheckpoint(t)
	if _, err := checkpoint.CommitMessage(42, 100); err != nil {
		t.Fatal(err)
	}
	backend := newScriptedBackend(t, false,
		expectRequest("SYNCMSG", checkSyncRequest(42, 100, 105), statusDocument(
			bson.E{Key: "chatLogs", Value: bson.A{
				bson.D{{Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "one"}},
				bson.D{{Key: "logId", Value: int64(103)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "three"}},
			}},
		)),
		expectRequest("SYNCMSG", checkSyncRequest(42, 103, 105), statusDocument(
			bson.E{Key: "chatLogs", Value: bson.A{
				bson.D{{Key: "logId", Value: int64(105)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "five"}},
			}},
		)),
	)
	api := testContinuityClient(t, checkpoint, backend)
	eventsOut, err := api.CatchUp(t.Context(), 42, 105)
	if err != nil {
		t.Fatal(err)
	}
	if len(eventsOut) != 3 {
		t.Fatalf("recovered events = %d", len(eventsOut))
	}
	for index, want := range []int64{101, 103, 105} {
		message, ok := eventsOut[index].(events.TextMessage)
		if !ok || message.LogID != want {
			t.Fatalf("event %d = %#v", index, eventsOut[index])
		}
	}
	if checkpoint.IsCommitted(42, 105) {
		t.Fatal("catch-up advanced checkpoint before application commit")
	}
	for _, event := range eventsOut {
		if err := api.CommitEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	if !checkpoint.IsCommitted(42, 105) {
		t.Fatal("explicit commit did not advance checkpoint")
	}
	backend.wait(t)
}

func TestCatchUpFailsOnNoProgress(t *testing.T) {
	checkpoint := testCheckpoint(t)
	if _, err := checkpoint.CommitMessage(42, 100); err != nil {
		t.Fatal(err)
	}
	backend := newScriptedBackend(t, false,
		expectRequest("SYNCMSG", checkSyncRequest(42, 100, 105), statusDocument(
			bson.E{Key: "chatLogs", Value: bson.A{}},
		)),
	)
	api := testContinuityClient(t, checkpoint, backend)
	if _, err := api.CatchUp(t.Context(), 42, 105); !errors.Is(err, ErrGapUnresolved) {
		t.Fatalf("CatchUp error = %v", err)
	}
	if !checkpoint.IsCommitted(42, 100) || checkpoint.IsCommitted(42, 101) {
		t.Fatal("failed catch-up changed checkpoint")
	}
	backend.wait(t)
}

func TestCommitEventRejectsPerChatReordering(t *testing.T) {
	checkpoint := testCheckpoint(t)
	api, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	api.checkpoint = checkpoint
	api.queueCommit(42, 101)
	api.queueCommit(42, 105)
	first := events.TextMessage{ChatID: 42, LogID: 101, Message: "first"}
	second := events.TextMessage{ChatID: 42, LogID: 105, Message: "second"}
	if err := api.CommitEvent(second); !errors.Is(err, ErrCommitOrder) {
		t.Fatalf("out-of-order commit error = %v", err)
	}
	if checkpoint.IsCommitted(42, 105) {
		t.Fatal("out-of-order event advanced checkpoint")
	}
	if err := api.CommitEvent(first); err != nil {
		t.Fatal(err)
	}
	if err := api.CommitEvent(second); err != nil {
		t.Fatal(err)
	}
}

func TestInitialSyncTargetsAreTypedAndSorted(t *testing.T) {
	api, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	document := func(chatID, logID int64) bson.Raw {
		return mustBSON(bson.D{
			{Key: "c", Value: chatID},
			{Key: "l", Value: bson.D{{Key: "logId", Value: logID}, {Key: "chatId", Value: chatID}}},
		})
	}
	api.dial = func(context.Context, authstate.State) (*Session, error) {
		return &Session{initialChatData: []bson.Raw{document(9, 90), document(3, 30)}}, nil
	}
	targets, err := api.InitialSyncTargets(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0].ChatID != 3 || targets[0].MaxLogID != 30 || targets[1].ChatID != 9 || targets[1].MaxLogID != 90 {
		t.Fatalf("targets = %#v", targets)
	}
}

func TestInitialSyncTargetsUsePersistedInventoryAfterEmptyDelta(t *testing.T) {
	checkpoint := testCheckpoint(t)
	firstToken := int64(41)
	if err := checkpoint.InstallSession(&firstToken, nil, []continuity.ChatTarget{{ChatID: 42, MaxLogID: 105}}, nil, true); err != nil {
		t.Fatal(err)
	}
	secondToken := int64(42)
	if err := checkpoint.InstallSession(&secondToken, nil, nil, nil, false); err != nil {
		t.Fatal(err)
	}
	api, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	api.checkpoint = checkpoint
	api.dial = func(context.Context, authstate.State) (*Session, error) {
		return &Session{initialChatData: []bson.Raw{}}, nil
	}
	targets, err := api.InitialSyncTargets(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0] != (syncmsg.Target{ChatID: 42, MaxLogID: 105}) {
		t.Fatalf("targets after empty delta = %#v", targets)
	}
	if chatIDs, _ := checkpoint.Snapshot().LoginCursors(); len(chatIDs) != 0 {
		t.Fatalf("uncommitted target leaked into LOGINLIST: %v", chatIDs)
	}
}

func checkSyncRequest(chatID, cur, max int64) func(bson.Raw) error {
	return func(raw bson.Raw) error {
		if err := requireExactKeys(raw, "chatId", "cur", "max", "cnt"); err != nil {
			return err
		}
		for key, want := range map[string]int64{"chatId": chatID, "cur": cur, "max": max} {
			if err := requireInt64(raw, key, want); err != nil {
				return err
			}
		}
		count, err := raw.LookupErr("cnt")
		if err != nil || count.Type != bson.TypeInt32 || count.Int32() != 300 {
			return errors.New("cnt is not int32(300)")
		}
		return nil
	}
}

func testCheckpoint(t *testing.T) *continuity.Store {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := continuity.Open(filepath.Join(dir, "checkpoint"))
	if err != nil {
		t.Fatal(err)
	}
	return checkpoint
}

func testContinuityClient(t *testing.T, checkpoint *continuity.Store, backend *scriptedBackend) *Client {
	t.Helper()
	api, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	api.checkpoint = checkpoint
	api.dial = func(context.Context, authstate.State) (*Session, error) {
		session := &Session{
			wire: backend.client, nextID: 1, pushes: make(chan loco.Packet, 4),
			pending: make(map[uint32]chan requestResult),
		}
		go session.readLoop()
		return session, nil
	}
	return api
}
