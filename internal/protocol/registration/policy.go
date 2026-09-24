package registration

import "time"

// Route is a confirmed registration operation path. It identifies an
// operation only; HTTP methods, encoding, headers, signing, and base URLs are
// intentionally outside this package.
type Route string

const (
	RoutePasscodeGenerate Route = "/mac/account/passcodeLogin/generate"
	RoutePasscodeRegister Route = "/mac/account/passcodeLogin/registerDevice"
	RoutePasscodeCancel   Route = "/mac/account/passcodeLogin/cancel"
	RouteQRGenerate       Route = "/mac/account/qrCodeLogin/generate"
	RouteQRCancel         Route = "/mac/account/qrCodeLogin/cancel"
	RouteQRLogin          Route = "/mac/account/qrCodeLogin/login"
	RouteQRPasswordCheck  Route = "/mac/account/qrCodeLogin/passwordCheck"
)

// CountdownTick is the cadence of visible passcode, QR, and device-auth
// countdowns.
const CountdownTick time.Duration = time.Second

// DefaultPollDelay is the conservative three-second scheduling policy used
// for initial QR polling and non-positive/fractional device-auth delays.
const DefaultPollDelay time.Duration = 3 * time.Second

// InitialQRPollDelay is the delay before the first QR approval poll.
const InitialQRPollDelay time.Duration = DefaultPollDelay

// MinimumPasscodePollDelay is the lower bound for passcode polling.
const MinimumPasscodePollDelay time.Duration = DefaultPollDelay

// DeviceAuthPollFallback is used for device-auth delays below one second.
// Positive delays at least one second remain server-directed.
const DeviceAuthPollFallback time.Duration = DefaultPollDelay

// DecodeQROutcome maps the confirmed QR login numeric result codes to semantic
// outcomes. Every unrecognized code, including negative values, fails closed
// as OutcomeUnknownFailure.
func DecodeQROutcome(code int) Outcome {
	return DecodeQROutcome64(int64(code))
}

// DecodeQROutcome64 is the width-stable form used by JSON decoders before any
// platform-sized integer conversion. Unknown values fail closed.
func DecodeQROutcome64(code int64) Outcome {
	switch code {
	case 1:
		return OutcomeUnregisteredDevice
	case 5:
		return OutcomeSuspended
	case 13:
		return OutcomeUnsupportedDevice
	case 14:
		return OutcomePending
	case 15:
		return OutcomeRejected
	case 16:
		return OutcomeExpired
	case 20:
		return OutcomeRestricted
	case 29:
		return OutcomeInvalidResponse
	default:
		return OutcomeUnknownFailure
	}
}

// ClampPasscodePollDelay applies the established three-second minimum to a
// server/controller delay while preserving longer delays.
func ClampPasscodePollDelay(delay time.Duration) time.Duration {
	if delay < MinimumPasscodePollDelay {
		return MinimumPasscodePollDelay
	}
	return delay
}

// QRPollDelay returns a positive server-directed QR delay as-is, and applies
// the three-second fallback to zero or negative values.
func QRPollDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return DefaultPollDelay
	}
	return delay
}

// DeviceAuthPollDelay returns a device-auth delay as-is when it is at least
// one second, and applies the three-second fallback below that boundary.
func DeviceAuthPollDelay(delay time.Duration) time.Duration {
	if delay < time.Second {
		return DeviceAuthPollFallback
	}
	return delay
}
