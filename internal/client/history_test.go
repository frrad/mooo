package client

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestHistoryPageKeepsLiveAndReadCheckpointsIndependent(t *testing.T) {
	checkpoint := testCheckpoint(t)
	if _, err := checkpoint.CommitMessage(42, 500); err != nil {
		t.Fatal(err)
	}
	if _, err := checkpoint.CommitReadWatermark(42, 400); err != nil {
		t.Fatal(err)
	}
	backend := newScriptedBackend(t, false, testloco.Expect("SYNCMSG", checkSyncRequest(42, 100, 103), statusDocument(bson.E{Key: "chatLogs", Value: bson.A{
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
	if err := api.CommitEvent(page.Events[0]); !errors.Is(err, errCommitOrder) {
		t.Fatalf("historical event entered live commit queue: %v", err)
	}
	waitBackends(t, backend)
}
func TestHistoryPageUnavailableDoesNotAdvance(t *testing.T) {
	checkpoint := testCheckpoint(t)
	backend := newScriptedBackend(t, false, testloco.Expect("SYNCMSG", checkSyncRequest(42, 100, 103), statusDocument()))
	api := testContinuityClient(t, checkpoint, backend)
	if _, err := api.ReadHistoryPage(t.Context(), 42, 100, 103, 3); !errors.Is(err, ErrGapUnresolved) || errors.Is(err, ErrHistoryBelowFloor) {
		t.Fatalf("unavailable history: %v", err)
	}
	waitBackends(t, backend)
}
func TestHistoryPageRejectsAnotherRoomBeforeReturningAnyEvents(t *testing.T) {
	checkpoint := testCheckpoint(t)
	backend := newScriptedBackend(t, false, testloco.Expect("SYNCMSG", checkSyncRequest(42, 100, 103), statusDocument(bson.E{Key: "chatLogs", Value: bson.A{
		bson.D{{Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "valid"}},
		bson.D{{Key: "chatId", Value: int64(43)}, {Key: "logId", Value: int64(103)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "foreign"}},
	}})))
	api := testContinuityClient(t, checkpoint, backend)
	if page, err := api.ReadHistoryPage(t.Context(), 42, 100, 103, 1); !errors.Is(err, events.ErrChatLogMisdirected) || len(page.Events) != 0 {
		t.Fatalf("foreign history escaped: %+v %v", page, err)
	}
	waitBackends(t, backend)
}

// The observed empty old window returned a floor above the selected ceiling.
// Keep that evidence distinct from an empty page whose cause is unknown.
func TestHistoryPageObservedFloorIsExplicitAndDoesNotAdvance(t *testing.T) {
	raw, err := os.ReadFile("../../research/fixtures/history-startup/observed-window.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Provenance struct {
			Kind string `json:"kind"`
		} `json:"provenance"`
		Cases []struct {
			Name  string `json:"name"`
			Input struct {
				ChatID int64 `json:"chatId"`
				Cur    int64 `json:"cur"`
				Max    int64 `json:"max"`
			}
			Expected json.RawMessage `json:"expected_projection"`
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Provenance.Kind != "observed" {
		t.Fatal("expected observed fixture provenance")
	}
	cases := 0
	for _, tc := range fixture.Cases {
		if !strings.HasPrefix(tc.Name, "old-interval-below-returned-floor-") {
			continue
		}
		cases++
		t.Run(tc.Name, func(t *testing.T) {
			var doc bson.D
			if err := bson.UnmarshalExtJSON(tc.Expected, false, &doc); err != nil {
				t.Fatal(err)
			}
			backend := newScriptedBackend(t, false, testloco.Expect("SYNCMSG", checkSyncRequest(tc.Input.ChatID, tc.Input.Cur, tc.Input.Max), doc))
			checkpoint := testCheckpoint(t)
			api := testContinuityClient(t, checkpoint, backend)
			page, err := api.ReadHistoryPage(t.Context(), tc.Input.ChatID, tc.Input.Cur, tc.Input.Max, 3)
			if !errors.Is(err, ErrHistoryBelowFloor) || !errors.Is(err, ErrGapUnresolved) {
				t.Fatalf("floor not reported: %+v %v", page, err)
			}
			if len(page.Events) != 0 || page.Complete || checkpoint.IsCommitted(tc.Input.ChatID, tc.Input.Max) || checkpoint.ReadWatermark(tc.Input.ChatID) != 0 {
				t.Fatal("unavailable history advanced progress")
			}
			waitBackends(t, backend)
		})
	}
	if cases != 2 {
		t.Fatalf("expected two observed floor cases, got %d", cases)
	}
}

func TestHistoryPageEmptyFloorAtOrBelowSelectionRemainsUnresolved(t *testing.T) {
	for _, floor := range []int64{0, 299, 300} {
		_, err := decodeHistoryPage(syncmsg.Request{ChatID: 5000, Cur: 199, Max: 300}, syncmsg.Response{MinLogID: floor}, 3, nil)
		if !errors.Is(err, ErrGapUnresolved) || errors.Is(err, ErrHistoryBelowFloor) {
			t.Fatalf("floor %d misclassified: %v", floor, err)
		}
	}
}
