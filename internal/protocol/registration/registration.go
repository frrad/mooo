// Package registration models the semantic state machine used by secondary
// device registration. It deliberately contains no transport, clock, storage,
// or credential handling. Callers supply events and execute the returned
// effects in their own layers.
package registration

// Outcome is the semantic result of an approval poll. Unknown wire values
// must be decoded as OutcomeUnknownFailure, or rejected before reaching this
// package; they must never be treated as approval.
type Outcome uint8

const (
	OutcomeNone Outcome = iota
	OutcomePending
	OutcomeApproved
	OutcomeUnregisteredDevice
	OutcomeRejected
	OutcomeExpired
	OutcomeUnsupportedDevice
	OutcomeSuspended
	OutcomeRestricted
	OutcomeInvalidResponse
	OutcomeUnknownFailure
)
