package sessionlogin

import (
	"errors"
	"reflect"
	"testing"
)

func TestSelectTokenHelperEffectsStrictAndGuarded(t *testing.T) {
	got, err := SelectTokenHelperEffects("token", 41, 42, true, true, "other_queue")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"compare_strict_greater", "dispatch_context_block", "wrap_write_operation", "invoke_block", "existing_loss_check_positive_guard", "existing_token_equality_probe", "set_loss_check_if_equal", "set_token", "process_changed_objects", "wait_for_operation"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("effects=%v want=%v", got, want)
	}
}

func TestSelectTokenHelperEffectsNoopAndContextGaps(t *testing.T) {
	got, err := SelectTokenHelperEffects("blind", 8, 8, false, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"no_effect"}) {
		t.Fatalf("effects=%v", got)
	}
	got, err = SelectTokenHelperEffects("token", 1, 2, false, false, "missing_queue")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"compare_strict_greater", "skip_block", "context_failure_behavior_unresolved"}) {
		t.Fatalf("effects=%v", got)
	}
}

func TestSelectTokenHelperEffectsAssertionAndBlindZero(t *testing.T) {
	if got, err := SelectTokenHelperEffects("token", -1, 0, false, false, ""); !errors.Is(err, ErrTokenCursorAssertion) || got != nil {
		t.Fatalf("got effects=%v err=%v", got, err)
	}
	got, err := SelectTokenHelperEffects("blind", -1, 0, false, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[len(got)-1] != "set_blind" {
		t.Fatalf("effects=%v", got)
	}
}
