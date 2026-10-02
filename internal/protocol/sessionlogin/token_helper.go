package sessionlogin

import "fmt"

// SelectTokenHelperEffects returns the ordered clean-room intents for the
// reviewed token-helper boundary. It performs no nested-context work, writes,
// commits, or profile/reset handling.
func SelectTokenHelperEffects(kind string, current, incoming int64, existingLossCheckPositive, existingTokenEqualsLossCheck bool, nestedContext string) ([]string, error) {
	if kind != "token" && kind != "blind" {
		return nil, fmt.Errorf("sessionlogin: unknown token helper kind %q", kind)
	}
	if kind == "token" && current < 0 && incoming == 0 {
		return nil, ErrTokenCursorAssertion
	}
	if incoming <= current {
		return []string{"no_effect"}, nil
	}
	effects := []string{"compare_strict_greater"}
	switch nestedContext {
	case "missing", "missing_queue":
		effects = append(effects, "skip_block", "context_failure_behavior_unresolved")
		return effects, nil
	case "current_queue":
		effects = append(effects, "dispatch_context_block", "invoke_block_inline")
	case "other_queue":
		effects = append(effects, "dispatch_context_block", "wrap_write_operation", "invoke_block")
	default:
		effects = append(effects, "open_nested_context", "dispatch_context_block")
	}
	if kind == "token" {
		if existingLossCheckPositive {
			effects = append(effects, "existing_loss_check_positive_guard", "existing_token_equality_probe")
			if existingTokenEqualsLossCheck {
				effects = append(effects, "set_loss_check_if_equal")
			}
		} else {
			effects = append(effects, "skip_loss_check_setter")
		}
		effects = append(effects, "set_token")
	} else {
		effects = append(effects, "set_blind")
	}
	if nestedContext == "other_queue" {
		effects = append(effects, "process_changed_objects", "wait_for_operation")
	}
	return effects, nil
}
