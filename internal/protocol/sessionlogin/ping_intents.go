package sessionlogin

// PingIntentKind identifies a planned lifecycle action. These intents are
// deliberately side-effect free: callers decide how to enqueue or dispatch
// each action in their own lifecycle owner.
type PingIntentKind string

const (
	PingIntentQueueCancel       PingIntentKind = "queue_cancel"
	PingIntentOrdinaryRequest   PingIntentKind = "ordinary_request"
	PingIntentQueueSchedule     PingIntentKind = "queue_schedule"
	PingIntentForwardCompletion PingIntentKind = "forward_completion"
)

// PingIntent is a planned action in the bounded reconnect lifecycle contract.
// Response and Err are opaque values preserved for a forwarding callback.
type PingIntent struct {
	Kind     PingIntentKind
	Response any
	Err      error
}

// PlanPingRequestEntered plans cancellation before an accepted ordinary
// request. An early rejection has no ordinary request or completion rearm,
// but forwards its immediate opaque result when a callback is present.
func PlanPingRequestEntered(accepted, callbackPresent bool, response any, err error) []PingIntent {
	intents := []PingIntent{{Kind: PingIntentQueueCancel}}
	if !accepted {
		if callbackPresent {
			intents = append(intents, PingIntent{
				Kind:     PingIntentForwardCompletion,
				Response: response,
				Err:      err,
			})
		}
		return intents
	}
	return append(intents, PingIntent{Kind: PingIntentOrdinaryRequest})
}

// PlanPingCompletion plans scheduling before forwarding an opaque transport
// completion when a callback is present. The scheduler and callback remain
// caller-owned; this function does not create timers or invoke callbacks.
func PlanPingCompletion(callbackPresent bool, response any, err error) []PingIntent {
	intents := []PingIntent{{Kind: PingIntentQueueSchedule}}
	if callbackPresent {
		intents = append(intents, PingIntent{
			Kind:     PingIntentForwardCompletion,
			Response: response,
			Err:      err,
		})
	}
	return intents
}

// PlanPingWithoutCompletion models the no-completion path: cancel, perform
// the ordinary request, then schedule the next lifecycle action.
func PlanPingWithoutCompletion() []PingIntent {
	return []PingIntent{
		{Kind: PingIntentQueueCancel},
		{Kind: PingIntentOrdinaryRequest},
		{Kind: PingIntentQueueSchedule},
	}
}
