package registration

// Route is a confirmed QR registration operation path.
type Route string

const (
	RouteQRGenerate Route = "/mac/account/qrCodeLogin/generate"
	RouteQRCancel   Route = "/mac/account/qrCodeLogin/cancel"
	RouteQRLogin    Route = "/mac/account/qrCodeLogin/login"
)

// RegistrationBaseURL is the reviewed registration service base URL.
const RegistrationBaseURL = "https://katalk.kakao.com"

// knownRoute rejects any route this package does not build, so a forged
// FormRequest cannot reach an unreviewed endpoint.
func knownRoute(route Route) bool {
	switch route {
	case RouteQRGenerate, RouteQRCancel, RouteQRLogin:
		return true
	default:
		return false
	}
}
