package sessionlogin

import "testing"

func TestKickoutOutsideAuthenticatedSessionHasNoLogoutEffects(t *testing.T) {
	state := NewRecoveryState(true, false, true)
	next, effects, err := ReduceRecovery(state, Kickout{Generation: 1, Reason: 10})
	if err != nil {
		t.Fatalf("unauthenticated KICKOUT error = %v, want nil", err)
	}
	if next != state {
		t.Fatalf("unauthenticated KICKOUT changed state: before=%#v after=%#v", state, next)
	}
	if len(effects) != 0 {
		t.Fatalf("unauthenticated KICKOUT effects = %v, want none", effects)
	}
}
