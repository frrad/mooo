package sessionlogin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func i64(value int64) TokenDirtyValue { return TokenDirtyValue{Int64: &value} }
func i32(value int32) TokenDirtyValue { return TokenDirtyValue{Int32: &value} }

func TestApplyTokenDirtyEventInitializationAndEqualObserver(t *testing.T) {
	state := &TokenDirtyState{}
	got, err := ApplyTokenDirtyEvent(state, TokenDirtyEvent{Initialization: "non_database", Field: "lastTokenId"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"initialization_defaults", "initialization_decode", "dirty_true", "nest_registration", "observer_registration"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("initialization effects=%v want=%v", got, want)
	}
	got, err = ApplyTokenDirtyEvent(state, TokenDirtyEvent{Initialization: "observer", Field: "lastTokenId", Old: i64(42), Incoming: i64(42)})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"observer_event_input", "no_changed_field_delta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("equal effects=%v want=%v", got, want)
	}
}

func TestApplyTokenDirtyEventOverwritesOriginalOldValue(t *testing.T) {
	state := &TokenDirtyState{}
	old := int32(7)
	if _, err := ApplyTokenDirtyEvent(state, TokenDirtyEvent{Initialization: "observer", Field: "lastBlindToken", Old: TokenDirtyValue{Int32: &old}, Incoming: i32(8)}); err != nil {
		t.Fatal(err)
	}
	old = 99
	if got := *state.OriginalOld["lastBlindToken"].Int32; got != 7 {
		t.Fatalf("recorded old value aliased input: got %d want 7", got)
	}
	if _, err := ApplyTokenDirtyEvent(state, TokenDirtyEvent{Initialization: "observer", Field: "lastBlindToken", Old: i32(8), Incoming: i32(9)}); err != nil {
		t.Fatal(err)
	}
	if got := *state.OriginalOld["lastBlindToken"].Int32; got != 8 {
		t.Fatalf("original old=%d want overwritten value 8", got)
	}
	if !state.Dirty || !state.Registered {
		t.Fatalf("state=%+v want dirty and registered", state)
	}
}

func TestApplyTokenDirtyEventRejectsWrongWidthAndUnknownKind(t *testing.T) {
	state := &TokenDirtyState{}
	if got, err := ApplyTokenDirtyEvent(state, TokenDirtyEvent{Initialization: "observer", Field: "lastBlindToken", Old: i64(1), Incoming: i64(2)}); err == nil || got != nil {
		t.Fatalf("wrong width got=%v err=%v", got, err)
	}
	if state.OriginalOld != nil || state.Dirty || state.Registered {
		t.Fatalf("invalid event mutated state: %+v", state)
	}
	if got, err := ApplyTokenDirtyEvent(state, TokenDirtyEvent{Initialization: "observer", Field: "unknown"}); err == nil || got != nil {
		t.Fatalf("unknown field got=%v err=%v", got, err)
	}
	if state.OriginalOld != nil || state.Dirty || state.Registered {
		t.Fatalf("unknown field mutated state: %+v", state)
	}
	if got, err := ApplyTokenDirtyEvent(&TokenDirtyState{}, TokenDirtyEvent{Initialization: "future", Field: "lastTokenId"}); err == nil || got != nil {
		t.Fatalf("unknown kind got=%v err=%v", got, err)
	}
}

func TestApplyTokenDirtyEventObservedVectors(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "token-dirty-unresolved.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors tokenDirtyVectors
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	valid := map[string]bool{
		"initialization_defaults": true, "initialization_decode": true, "dirty_true": true,
		"nest_registration": true, "observer_registration": true, "observer_event_input": true,
		"old_value_recorded": true, "no_changed_field_delta": true,
	}
	for _, vector := range vectors.Vectors {
		for _, expected := range vector.Expect {
			if !valid[expected] && vector.Execution == "observed" {
				t.Fatalf("%q: unknown observed effect %q", vector.Name, expected)
			}
		}
		if vector.Execution != "observed" {
			continue
		}
		event := TokenDirtyEvent{Initialization: vector.Initialization, Field: vector.Field}
		if vector.Old != nil || vector.Incoming != nil {
			event.Initialization = "observer"
			if vector.Old == nil || vector.Incoming == nil {
				t.Fatalf("%q: observer vector requires old and incoming values", vector.Name)
			}
			if vector.Field == "lastBlindToken" {
				if vector.Old == nil || vector.Incoming == nil || *vector.Old < -1<<31 || *vector.Old > 1<<31-1 || *vector.Incoming < -1<<31 || *vector.Incoming > 1<<31-1 {
					t.Fatalf("%q: invalid blind vector values", vector.Name)
				}
				old, incoming := int32(*vector.Old), int32(*vector.Incoming)
				event.Old, event.Incoming = i32(old), i32(incoming)
			} else {
				event.Old, event.Incoming = i64(*vector.Old), i64(*vector.Incoming)
			}
		}
		got, err := ApplyTokenDirtyEvent(&TokenDirtyState{}, event)
		if err != nil {
			t.Fatalf("%q: %v", vector.Name, err)
		}
		for _, expected := range vector.Expect {
			found := false
			for i, effect := range got {
				if effect == expected {
					got = got[i+1:]
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%q: expected ordered effect %q in %v", vector.Name, expected, got)
			}
		}
	}
}

func TestApplyTokenDirtyEventEqualObserverLeavesStateUnchanged(t *testing.T) {
	nilState := &TokenDirtyState{}
	if _, err := ApplyTokenDirtyEvent(nilState, TokenDirtyEvent{Initialization: "observer", Field: "lastTokenId", Old: i64(5), Incoming: i64(5)}); err != nil || nilState.OriginalOld != nil || nilState.Dirty || nilState.Registered {
		t.Fatalf("equal observer mutated nil state: %+v err=%v", nilState, err)
	}
	state := &TokenDirtyState{OriginalOld: map[string]TokenDirtyValue{"lastTokenId": i64(3)}}
	before := *state
	beforeMap := map[string]TokenDirtyValue{"lastTokenId": i64(3)}
	got, err := ApplyTokenDirtyEvent(state, TokenDirtyEvent{Initialization: "observer", Field: "lastTokenId", Old: i64(42), Incoming: i64(42)})
	if err != nil || !reflect.DeepEqual(got, []string{"observer_event_input", "no_changed_field_delta"}) {
		t.Fatalf("equal observer effects=%v err=%v", got, err)
	}
	if state.Dirty != before.Dirty || state.Registered != before.Registered || !reflect.DeepEqual(state.OriginalOld, beforeMap) {
		t.Fatalf("equal observer changed state: %+v", state)
	}
}

func TestApplyTokenDirtyEventAcceptsSigned64LossCheckBounds(t *testing.T) {
	for _, values := range [][2]int64{{-1 << 63, -1<<63 + 1}, {1<<63 - 2, 1<<63 - 1}} {
		got, err := ApplyTokenDirtyEvent(&TokenDirtyState{}, TokenDirtyEvent{Initialization: "observer", Field: "lastLossCheckLogId", Old: i64(values[0]), Incoming: i64(values[1])})
		if err != nil || len(got) != 4 {
			t.Fatalf("loss-check values %v effects=%v err=%v", values, got, err)
		}
	}
}
