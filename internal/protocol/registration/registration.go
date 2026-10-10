// Package registration implements the logged-out Mac QR device-registration
// HTTP exchange: request models and their JSON builders for QR generate, poll
// and cancel; bounded, redacting decoders for the generate, login-success and
// server-error responses; the Mac header policy for these requests; a
// single-attempt executor over an injected macweb.Doer; and
// QRRegistrationService, which composes them. It does not own an HTTP client,
// retries, timers, credential storage or the login state machine; the
// connector supplies those.
package registration

// Outcome classifies a QR approval-poll server status. Only the statuses the
// connector acts on are named; every other value, including unknown ones,
// is the zero Outcome and must be treated as a terminal failure, never as
// approval. research/device-registration/PROTOCOL.md lists the full status
// table.
type Outcome uint8

const (
	outcomeTerminal Outcome = iota
	OutcomePending
	OutcomeUnregisteredDevice
)

// qrOutcome maps a decoded integer status. Unknown values fail closed.
func qrOutcome(code int64) Outcome {
	switch code {
	case 1, -100:
		return OutcomeUnregisteredDevice
	case -150, 14:
		return OutcomePending
	default:
		return outcomeTerminal
	}
}
