package registration

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

// DecodeQROutcome64 is the width-stable form used by JSON decoders before any
// platform-sized integer conversion. Unknown values fail closed.
func DecodeQROutcome64(code int64) Outcome {
	switch code {
	case 1, -100:
		return OutcomeUnregisteredDevice
	case 5:
		return OutcomeSuspended
	case 13:
		return OutcomeUnsupportedDevice
	case -150, 14:
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
