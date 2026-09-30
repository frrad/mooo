package events

import (
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestReduceChatStatusAppliesOnlyNewerExistingRoomState(t *testing.T) {
	status := bson.D{{Key: "synthetic", Value: "opaque"}}
	statusBytes, err := bson.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	change := ChatStatusChanged{ChatID: 42, Revision: 9, Status: statusBytes}
	initial := ChatStatusState{
		RoomExists: true,
		Revision:   7,
		ExtraInfo:  bson.D{{Key: "existing", Value: "preserve"}},
	}

	result := ReduceChatStatus(initial, change)
	if !result.Applied {
		t.Fatal("newer status for an existing room was not applied")
	}
	if result.State.Revision != 9 {
		t.Fatalf("revision = %d, want 9", result.State.Revision)
	}
	wantExtra := bson.D{
		{Key: "existing", Value: "preserve"},
		{Key: "cs", Value: bson.Raw(statusBytes)},
		{Key: "csr", Value: int64(9)},
	}
	if !reflect.DeepEqual(result.State.ExtraInfo, wantExtra) {
		t.Fatalf("extra info = %#v, want %#v", result.State.ExtraInfo, wantExtra)
	}
}

func TestReduceChatStatusReplacesExistingStatusFieldsAndOwnsStatus(t *testing.T) {
	statusBytes, err := bson.Marshal(bson.D{{Key: "synthetic", Value: "new"}})
	if err != nil {
		t.Fatal(err)
	}
	initial := ChatStatusState{
		RoomExists: true,
		Revision:   7,
		ExtraInfo: bson.D{
			{Key: "cs", Value: bson.Raw{1, 2, 3}},
			{Key: "keep", Value: "value"},
			{Key: "csr", Value: int64(7)},
			{Key: "cs", Value: bson.Raw{4, 5, 6}},
		},
	}
	result := ReduceChatStatus(initial, ChatStatusChanged{Revision: 9, Status: statusBytes})
	if !result.Applied {
		t.Fatal("newer status was not applied")
	}
	if got := countExtraKey(result.State.ExtraInfo, "cs"); got != 1 {
		t.Fatalf("cs field count = %d, want 1", got)
	}
	if got := countExtraKey(result.State.ExtraInfo, "csr"); got != 1 {
		t.Fatalf("csr field count = %d, want 1", got)
	}
	statusField := findExtra(result.State.ExtraInfo, "cs")
	status, ok := statusField.Value.(bson.Raw)
	if !ok || string(status) != string(statusBytes) {
		t.Fatalf("cs = %#v, want owned status bytes", statusField.Value)
	}
	original := append([]byte(nil), status...)
	statusBytes[0] ^= 0xff
	if string(status) != string(original) {
		t.Fatal("status output unexpectedly aliases input")
	}
}

func countExtraKey(extra bson.D, key string) int {
	count := 0
	for _, field := range extra {
		if field.Key == key {
			count++
		}
	}
	return count
}

func findExtra(extra bson.D, key string) bson.E {
	for _, field := range extra {
		if field.Key == key {
			return field
		}
	}
	return bson.E{}
}

func TestReduceChatStatusRejectsMissingRoomAndStaleOrDuplicateRevision(t *testing.T) {
	statusBytes, err := bson.Marshal(bson.D{{Key: "synthetic", Value: "opaque"}})
	if err != nil {
		t.Fatal(err)
	}
	change := ChatStatusChanged{ChatID: 42, Revision: 9, Status: statusBytes}
	for _, test := range []struct {
		name  string
		state ChatStatusState
	}{
		{name: "missing room", state: ChatStatusState{Revision: 1}},
		{name: "stale revision", state: ChatStatusState{RoomExists: true, Revision: 10}},
		{name: "duplicate revision", state: ChatStatusState{RoomExists: true, Revision: 9}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := ReduceChatStatus(test.state, change)
			if result.Applied {
				t.Fatalf("transition = %#v, want no persistence", result)
			}
			if !reflect.DeepEqual(result.State, test.state) {
				t.Fatalf("state = %#v, want unchanged %#v", result.State, test.state)
			}
		})
	}
}

func TestReduceChatStatusRejectsMissingStatus(t *testing.T) {
	initial := ChatStatusState{RoomExists: true, Revision: 7, ExtraInfo: bson.D{{Key: "keep", Value: true}}}
	result := ReduceChatStatus(initial, ChatStatusChanged{ChatID: 42, Revision: 9})
	if result.Applied {
		t.Fatalf("transition = %#v, want no persistence without chatStatus", result)
	}
	if !reflect.DeepEqual(result.State, initial) {
		t.Fatalf("state = %#v, want unchanged %#v", result.State, initial)
	}
}
