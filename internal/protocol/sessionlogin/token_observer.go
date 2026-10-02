package sessionlogin

import "fmt"

// TokenDirtyValue carries one model field value using the field's declared
// signed width. It is intentionally a value-only type: persistence and
// automatic notification delivery are outside this boundary.
type TokenDirtyValue struct {
	Int64 *int64
	Int32 *int32
}

// TokenDirtyEvent describes either model initialization or an explicit
// observer callback. Initialization and observer delivery are separate inputs
// because a setter's automatic notification policy is unresolved.
type TokenDirtyEvent struct {
	Initialization string
	Field          string
	Old            TokenDirtyValue
	Incoming       TokenDirtyValue
}

// TokenDirtyState is the minimal clean-room state needed to model the
// observed old-value, dirty, and changed-object registration effects.
type TokenDirtyState struct {
	OriginalOld map[string]TokenDirtyValue
	Dirty       bool
	Registered  bool
}

// ApplyTokenDirtyEvent returns observed effects and updates only the
// in-memory observer state. Each changed callback overwrites OriginalOld for
// its field, matching the observed callback rather than assuming first-write
// retention.
func ApplyTokenDirtyEvent(state *TokenDirtyState, event TokenDirtyEvent) ([]string, error) {
	if state == nil {
		return nil, fmt.Errorf("sessionlogin: nil token dirty state")
	}
	if err := validateTokenDirtyField(event.Field); err != nil {
		return nil, err
	}
	switch event.Initialization {
	case "non_database":
		if event.Old != (TokenDirtyValue{}) || event.Incoming != (TokenDirtyValue{}) {
			return nil, fmt.Errorf("sessionlogin: initialization event carries observer values")
		}
		if state.OriginalOld == nil {
			state.OriginalOld = make(map[string]TokenDirtyValue)
		}
		state.Dirty = true
		state.Registered = true
		return []string{"initialization_defaults", "initialization_decode", "dirty_true", "nest_registration", "observer_registration"}, nil
	case "database":
		if event.Old != (TokenDirtyValue{}) || event.Incoming != (TokenDirtyValue{}) {
			return nil, fmt.Errorf("sessionlogin: initialization event carries observer values")
		}
		if state.OriginalOld == nil {
			state.OriginalOld = make(map[string]TokenDirtyValue)
		}
		return []string{"initialization_decode", "observer_registration"}, nil
	case "observer":
		if err := validateTokenDirtyValue(event.Field, event.Old); err != nil {
			return nil, err
		}
		if err := validateTokenDirtyValue(event.Field, event.Incoming); err != nil {
			return nil, err
		}
		if state.OriginalOld == nil {
			state.OriginalOld = make(map[string]TokenDirtyValue)
		}
		effects := []string{"observer_event_input"}
		if tokenDirtyValuesEqual(event.Field, event.Old, event.Incoming) {
			return append(effects, "no_changed_field_delta"), nil
		}
		state.OriginalOld[event.Field] = cloneTokenDirtyValue(event.Field, event.Old)
		state.Dirty = true
		state.Registered = true
		return append(effects, "old_value_recorded", "dirty_true", "nest_registration"), nil
	default:
		return nil, fmt.Errorf("sessionlogin: unknown token dirty event kind %q", event.Initialization)
	}
}

func validateTokenDirtyField(field string) error {
	switch field {
	case "lastTokenId", "lastLossCheckLogId", "lastBlindToken":
		return nil
	default:
		return fmt.Errorf("sessionlogin: unknown token dirty field %q", field)
	}
}

func validateTokenDirtyValue(field string, value TokenDirtyValue) error {
	switch field {
	case "lastTokenId", "lastLossCheckLogId":
		if value.Int64 == nil || value.Int32 != nil {
			return fmt.Errorf("sessionlogin: %s requires signed-64 value", field)
		}
	case "lastBlindToken":
		if value.Int32 == nil || value.Int64 != nil {
			return fmt.Errorf("sessionlogin: %s requires signed-32 value", field)
		}
	}
	return nil
}

func tokenDirtyValuesEqual(field string, left, right TokenDirtyValue) bool {
	if field == "lastBlindToken" {
		return *left.Int32 == *right.Int32
	}
	return *left.Int64 == *right.Int64
}

func cloneTokenDirtyValue(field string, value TokenDirtyValue) TokenDirtyValue {
	if field == "lastBlindToken" {
		copy := *value.Int32
		return TokenDirtyValue{Int32: &copy}
	}
	copy := *value.Int64
	return TokenDirtyValue{Int64: &copy}
}
