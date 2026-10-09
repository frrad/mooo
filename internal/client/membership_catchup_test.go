package client

import (
	"github.com/frrad/mooo/internal/protocol/events"
	"go.mongodb.org/mongo-driver/v2/bson"
	"testing"
)

// Observed owned-account membership-only SYNCMSG pages include a later feed
// beyond the login ceiling. Keep catch-up bounded to the requested interval.
func TestMembershipFeedBeyondCatchUpCeilingDoesNotBreakBoundedRecovery(t *testing.T) {
	checkpoint := testCheckpoint(t)
	if _, err := checkpoint.CommitMessage(42, 100); err != nil {
		t.Fatal(err)
	}
	backend := newScriptedBackend(t, false, expectRequest("SYNCMSG", checkSyncRequest(42, 100, 101), statusDocument(bson.E{Key: "chatLogs", Value: bson.A{
		bson.D{{Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "synthetic boundary"}},
		bson.D{{Key: "logId", Value: int64(102)}, {Key: "type", Value: int32(0)}, {Key: "message", Value: `{"feedType":2,"member":{"userId":7},"kicked":false,"hidden":false,"memorial":false}`}},
	}})))
	api := testContinuityClient(t, checkpoint, backend)
	recovered, err := api.CatchUp(t.Context(), 42, 101)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 {
		t.Fatalf("recovered %d events beyond selected ceiling", len(recovered))
	}
	if msg, ok := recovered[0].(events.TextMessage); !ok || msg.LogID != 101 {
		t.Fatalf("boundary %T", recovered[0])
	}
	if checkpoint.IsCommitted(42, 101) {
		t.Fatal("retrieval committed before delivery")
	}
	if err = api.CommitEvent(recovered[0]); err != nil {
		t.Fatal(err)
	}
	if checkpoint.IsCommitted(42, 102) {
		t.Fatal("out-of-interval feed advanced cursor")
	}
	backend.wait(t)
}
