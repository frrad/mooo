package client

import (
	"context"
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

func TestFirstUncommittedLiveMessageRemainsResumeTargetAfterRestart(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "checkpoint")
	checkpoint, err := continuity.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	token := int64(7)
	if err := checkpoint.InstallSession(&token, nil, []continuity.ChatTarget{{ChatID: 42, MaxLogID: 105}}, nil, true); err != nil {
		t.Fatal(err)
	}
	api, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	api.checkpoint = checkpoint
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "first live delivery"}}}})
	if err != nil {
		t.Fatal(err)
	}
	raw := make(chan loco.Packet, 1)
	raw <- loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body}
	close(raw)
	output := make(chan events.Result, 1)
	decodeEventStreamWithContinuity(raw, output, checkpoint, api.queueCommit)
	result := <-output
	if result.Err != nil || result.Event == nil {
		t.Fatal("first message was not admitted")
	}
	// Simulate failed application delivery and process restart without CommitEvent.
	reopened, err := continuity.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Snapshot().Chats) != 0 {
		t.Fatal("admission fabricated an application commit")
	}
	restarted, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	restarted.checkpoint = reopened
	restarted.dial = func(context.Context, authstate.State) (*Session, error) {
		return &Session{initialChatData: []bson.Raw{}}, nil
	}
	targets, err := restarted.ResumeTargets(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0] != (syncmsg.Target{ChatID: 42, MaxLogID: 105}) {
		t.Fatal("first failed live delivery disappeared from restart recovery")
	}
	ids, _ := reopened.Snapshot().LoginCursors()
	if len(ids) != 0 || reopened.ReadWatermark(42) != 0 {
		t.Fatal("pending admission leaked into acknowledgement state")
	}
	backend := newScriptedBackend(t, false, expectRequest("SYNCMSG", checkSyncRequest(42, 100, 105), statusDocument(bson.E{Key: "chatLogs", Value: bson.A{
		bson.D{{Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "first live delivery"}},
		bson.D{{Key: "logId", Value: int64(105)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "later"}},
	}})))
	replay := testContinuityClient(t, reopened, backend)
	missed, err := replay.CatchUp(t.Context(), 42, 105)
	if err != nil || len(missed) != 2 {
		t.Fatal("bounded replay from the admitted floor failed")
	}
	if len(reopened.Snapshot().DeliveryStarts) != 1 {
		t.Fatal("retrieval cleared uncommitted admission")
	}
	for _, message := range missed {
		if err := replay.CommitEvent(message); err != nil {
			t.Fatal(err)
		}
	}
	if len(reopened.Snapshot().DeliveryStarts) != 0 || !reopened.IsCommitted(42, 105) {
		t.Fatal("confirmed replay did not transition to committed progress")
	}
}

func TestAdmissionPersistenceFailureStopsBeforeApplicationDelivery(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "checkpoint")
	checkpoint, err := continuity.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "synthetic"}}}})
	if err != nil {
		t.Fatal(err)
	}
	raw := make(chan loco.Packet, 1)
	raw <- loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body}
	close(raw)
	output := make(chan events.Result, 2)
	delivered, terminated := 0, 0
	decodeEventStreamWithTerminalStop(raw, output, checkpoint, func(int64, int64) { delivered++ }, func() { terminated++ }, nil)
	result := <-output
	if result.Err == nil || result.Event != nil || delivered != 0 || terminated != 1 {
		t.Fatal("failed durable admission reached application delivery")
	}
	if _, ok := <-output; ok {
		t.Fatal("decoder continued after persistence failure")
	}
	if len(checkpoint.Snapshot().DeliveryStarts) != 0 {
		t.Fatal("failed write changed in-memory admission state")
	}
}
