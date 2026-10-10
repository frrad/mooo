package client

import (
	"errors"
	"github.com/frrad/mooo/internal/protocol/events"
	"go.mongodb.org/mongo-driver/v2/bson"
	"testing"
)

func TestHistoryPageKeepsLiveAndReadCheckpointsIndependent(t *testing.T) {
	checkpoint := testCheckpoint(t)
	if _, err := checkpoint.CommitMessage(42, 500); err != nil {
		t.Fatal(err)
	}
	if _, err := checkpoint.CommitReadWatermark(42, 400); err != nil {
		t.Fatal(err)
	}
	backend := newScriptedBackend(t, false, expectRequest("SYNCMSG", checkSyncRequest(42, 100, 103), statusDocument(bson.E{Key: "chatLogs", Value: bson.A{
		bson.D{{Key: "logId", Value: int64(100)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "inclusive boundary"}},
		bson.D{{Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "one"}},
		bson.D{{Key: "logId", Value: int64(103)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "three"}},
	}})))
	api := testContinuityClient(t, checkpoint, backend)
	page, err := api.ReadHistoryPage(t.Context(), 42, 100, 103, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Next != 101 || page.Complete {
		t.Fatalf("page: %+v", page)
	}
	if got := page.Events[0].(events.TextMessage).Message; got != "one" {
		t.Fatal("wrong historical text")
	}
	if checkpoint.ReadWatermark(42) != 400 || !checkpoint.IsCommitted(42, 500) {
		t.Fatal("history changed live/read state")
	}
	if err := api.CommitEvent(page.Events[0]); !errors.Is(err, ErrCommitOrder) {
		t.Fatalf("historical event entered live commit queue: %v", err)
	}
	backend.wait(t)
}
func TestHistoryPageUnavailableDoesNotAdvance(t *testing.T) {
	checkpoint := testCheckpoint(t)
	backend := newScriptedBackend(t, false, expectRequest("SYNCMSG", checkSyncRequest(42, 100, 103), statusDocument()))
	api := testContinuityClient(t, checkpoint, backend)
	if _, err := api.ReadHistoryPage(t.Context(), 42, 100, 103, 3); !errors.Is(err, ErrGapUnresolved) {
		t.Fatalf("unavailable history: %v", err)
	}
	backend.wait(t)
}
func TestHistoryPageRejectsAnotherRoomBeforeReturningAnyEvents(t *testing.T) {
	checkpoint := testCheckpoint(t)
	backend := newScriptedBackend(t, false, expectRequest("SYNCMSG", checkSyncRequest(42, 100, 103), statusDocument(bson.E{Key: "chatLogs", Value: bson.A{
		bson.D{{Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "valid"}},
		bson.D{{Key: "chatId", Value: int64(43)}, {Key: "logId", Value: int64(103)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "foreign"}},
	}})))
	api := testContinuityClient(t, checkpoint, backend)
	if page, err := api.ReadHistoryPage(t.Context(), 42, 100, 103, 1); !errors.Is(err, ErrProtocol) || len(page.Events) != 0 {
		t.Fatalf("foreign history escaped: %+v %v", page, err)
	}
	backend.wait(t)
}
