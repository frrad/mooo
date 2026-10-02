package sessionlogin

import (
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
	if want := []string{"initialization_defaults", "dirty_true", "nest_registration"}; !reflect.DeepEqual(got, want) {
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
	if _, err := ApplyTokenDirtyEvent(state, TokenDirtyEvent{Initialization: "observer", Field: "lastBlindToken", Old: i32(7), Incoming: i32(8)}); err != nil {
		t.Fatal(err)
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
	if got, err := ApplyTokenDirtyEvent(&TokenDirtyState{}, TokenDirtyEvent{Initialization: "observer", Field: "lastBlindToken", Old: i64(1), Incoming: i64(2)}); err == nil || got != nil {
		t.Fatalf("wrong width got=%v err=%v", got, err)
	}
	if got, err := ApplyTokenDirtyEvent(&TokenDirtyState{}, TokenDirtyEvent{Initialization: "future", Field: "lastTokenId"}); err == nil || got != nil {
		t.Fatalf("unknown kind got=%v err=%v", got, err)
	}
}
