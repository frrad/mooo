package registration

// RegistrationBaseURL is the reviewed registration service base URL. The
// profile is descriptive only; callers still own transport, cookies, defaults,
// and request encoding.
const RegistrationBaseURL = "https://katalk.kakao.com"

// HTTPMethod is the semantic method required by the reviewed registration
// profile. It is not a request encoder.
type HTTPMethod string

const HTTPMethodPost HTTPMethod = "POST"

// DeviceShape describes the nested device dictionary required by an operation.
type DeviceShape uint8

const (
	DeviceNone DeviceShape = iota
	DeviceUUIDOnly
	DeviceFull
)

// FieldMask identifies semantic form fields without prescribing URL-form
// encoding or byte ordering.
type FieldMask uint16

const (
	FieldEmail FieldMask = 1 << iota
	FieldPassword
	FieldPermanent
	FieldPreviousID
	FieldID
)

// FormStructure describes the nested form fields for a confirmed route.
type FormStructure struct {
	Fields FieldMask
	Device DeviceShape
}

// HTTPRequestProfile is metadata for one registration operation. It contains
// no request values, secrets, serialized bytes, or transport behavior.
type HTTPRequestProfile struct {
	BaseURL string
	Method  HTTPMethod
	Route   Route
	Form    FormStructure
}

// ProfileFor returns the reviewed profile for a route. Unknown routes are
// rejected rather than receiving guessed fields.
func ProfileFor(route Route) (HTTPRequestProfile, bool) {
	profile := HTTPRequestProfile{
		BaseURL: RegistrationBaseURL,
		Method:  HTTPMethodPost,
		Route:   route,
	}
	switch route {
	case RoutePasscodeGenerate:
		profile.Form = FormStructure{Fields: FieldEmail | FieldPassword | FieldPermanent, Device: DeviceFull}
	case RoutePasscodeRegister, RoutePasscodeCancel:
		profile.Form = FormStructure{Fields: FieldEmail | FieldPassword, Device: DeviceUUIDOnly}
	case RouteQRGenerate:
		profile.Form = FormStructure{Fields: FieldPreviousID, Device: DeviceFull}
	case RouteQRCancel, RouteQRLogin:
		profile.Form = FormStructure{Fields: FieldID, Device: DeviceUUIDOnly}
	case RouteQRPasswordCheck:
		profile.Form = FormStructure{Fields: FieldPassword, Device: DeviceNone}
	default:
		return HTTPRequestProfile{}, false
	}
	return profile, true
}

// PasswordCheckResponse is the semantic response subset needed by the
// password-check predicate. StatusPresent represents dictionary key presence,
// including an explicit zero value.
type PasswordCheckResponse struct {
	StatusPresent bool
	Status        int32
}

// PasswordCheckSucceeded applies the reviewed response predicate: HTTP 200, a
// response dictionary, an integer status, and status zero. It intentionally
// does not accept arbitrary JSON values or infer success from missing fields.
func PasswordCheckSucceeded(httpStatus int, response *PasswordCheckResponse) bool {
	return httpStatus == 200 && response != nil && response.StatusPresent && response.Status == 0
}
