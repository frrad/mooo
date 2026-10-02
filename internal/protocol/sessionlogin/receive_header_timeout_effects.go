package sessionlogin

import (
	"fmt"
	"time"
)

const receiveHeaderTimeoutSelector = "fireReceiveHeaderTimeout:"

// ReceiveHeaderTimeoutInput keeps the admission-time configuration read
// separate from the queued execution read. The latter may change, including
// to zero, between admission and execution.
type ReceiveHeaderTimeoutInput struct {
	AdmissionTimeout time.Duration
	ExecutionTimeout time.Duration
	EnableByte       byte
	Owner            string
	RequestTag       int64
}

type ReceiveHeaderTimeoutEffect struct {
	Kind   string
	Delay  time.Duration
	Owner  string
	Tag    int64
	Target string
}

// PlanReceiveHeaderTimeout returns the bounded admission and queued effects
// recovered from the receive-header timeout helper. It models no queue,
// socket, completion, or durable state behavior.
func PlanReceiveHeaderTimeout(input ReceiveHeaderTimeoutInput) ([]ReceiveHeaderTimeoutEffect, error) {
	if input.Owner == "" {
		return nil, fmt.Errorf("sessionlogin: timeout owner is required")
	}
	if input.AdmissionTimeout <= 0 || input.RequestTag < 0 {
		return []ReceiveHeaderTimeoutEffect{{Kind: "no_enqueue"}}, nil
	}
	effects := []ReceiveHeaderTimeoutEffect{
		{Kind: "read_timeout", Delay: input.AdmissionTimeout},
		{Kind: "check_tag_nonnegative", Tag: input.RequestTag},
		{Kind: "queue_main"},
	}
	if input.EnableByte == 1 {
		effects = append(effects,
			ReceiveHeaderTimeoutEffect{Kind: "reread_timeout", Delay: input.ExecutionTimeout},
			ReceiveHeaderTimeoutEffect{Kind: "perform_selector_after_delay", Delay: input.ExecutionTimeout},
			ReceiveHeaderTimeoutEffect{Kind: "owner_target", Owner: input.Owner},
			ReceiveHeaderTimeoutEffect{Kind: "fire_selector", Target: receiveHeaderTimeoutSelector},
			ReceiveHeaderTimeoutEffect{Kind: "wrapped_tag", Tag: input.RequestTag},
		)
		return effects, nil
	}
	effects = append(effects,
		ReceiveHeaderTimeoutEffect{Kind: "cancel_previous_perform"},
		ReceiveHeaderTimeoutEffect{Kind: "owner_target", Owner: input.Owner},
		ReceiveHeaderTimeoutEffect{Kind: "fire_selector", Target: receiveHeaderTimeoutSelector},
		ReceiveHeaderTimeoutEffect{Kind: "wrapped_tag", Tag: input.RequestTag},
	)
	return effects, nil
}
