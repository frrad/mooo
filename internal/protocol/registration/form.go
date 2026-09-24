package registration

import (
	"errors"
	"sort"
	"strings"
	"unicode/utf8"
)

// RegistrationFormContentType is the body content type used by the reviewed
// Alamofire URL-form request profile.
const RegistrationFormContentType = "application/x-www-form-urlencoded; charset=utf-8"

var (
	// ErrInvalidFormRequest is deliberately static so request validation cannot
	// echo a password, identifier, or other caller-provided value.
	ErrInvalidFormRequest = errors.New("registration: invalid form request")
	// ErrMissingFormField identifies a missing required semantic value without
	// including the value itself in an error or log message.
	ErrMissingFormField = errors.New("registration: missing required form field")
	// ErrFormTooLarge is static and does not disclose which value exceeded the
	// defensive field/body limits.
	ErrFormTooLarge = errors.New("registration: form too large")
)

// These are defensive serializer limits, not claims about server-side field
// grammar. They bound memory and encoded body size before any future transport
// is allowed to submit a request.
const (
	MaxFormFieldBytes = 4 * 1024
	MaxFormBodyBytes  = 64 * 1024
)

// FullDevice is the reviewed nested device dictionary used by QR generation
// and passcode generation. It contains caller-owned metadata only; this
// package does not discover or import official-client identity.
type FullDevice struct {
	Name      string
	UUID      string
	OSVersion string
	Model     string
}

func (d FullDevice) validate() error {
	for _, value := range []string{d.Name, d.UUID, d.OSVersion, d.Model} {
		if err := validateRequiredFormString(value); err != nil {
			return err
		}
	}
	return nil
}

// UUIDOnlyDevice is the reviewed nested device dictionary for poll, cancel,
// and passcode-register operations.
type UUIDOnlyDevice struct {
	UUID string
}

func (d UUIDOnlyDevice) validate() error { return validateRequiredFormString(d.UUID) }

// QRGenerateRequest is the reviewed QR generation body. PreviousID is
// optional; a nil pointer omits previousId, while a non-nil pointer preserves
// an explicitly supplied (including empty) value.
type QRGenerateRequest struct {
	PreviousID *string
	Device     FullDevice
}

// FormRequest is a transport-neutral serialized request. It describes the
// reviewed route and body only; callers still own HTTP clients, cookies,
// ordinary platform headers, deadlines, and response decoding.
type FormRequest struct {
	Profile     HTTPRequestProfile
	ContentType string
	Body        []byte
}

// BuildQRGenerateRequest validates and encodes one QR generation request.
// Encoding follows the reviewed Alamofire URLEncoding.httpBody behavior:
// sorted keys, nested bracket keys, and RFC3986 percent escaping.
// The builder covers only request serialization; response decoding and
// operation sequencing remain outside this package.
func BuildQRGenerateRequest(request QRGenerateRequest) (FormRequest, error) {
	if err := request.Device.validate(); err != nil {
		return FormRequest{}, err
	}
	if request.PreviousID != nil {
		if err := validateOptionalFormString(*request.PreviousID); err != nil {
			return FormRequest{}, err
		}
	}
	values := formObject{
		"device": fullDeviceFormValue(request.Device),
	}
	if request.PreviousID != nil {
		values["previousId"] = formString(*request.PreviousID)
	}
	return buildFormRequest(RouteQRGenerate, values)
}

// PasscodeGenerateRequest covers the directly evidenced passcode-generation
// body. Its response/status and authorization sequencing remain out of scope.
type PasscodeGenerateRequest struct {
	Email     string
	Password  string
	Permanent bool
	Device    FullDevice
}

// BuildPasscodeGenerateRequest validates and encodes one passcode generation
// request. Alamofire's default BoolEncoding serializes false/true as numeric
// 0/1 values.
func BuildPasscodeGenerateRequest(request PasscodeGenerateRequest) (FormRequest, error) {
	if err := validateRequiredFormString(request.Email); err != nil {
		return FormRequest{}, err
	}
	if err := validateRequiredFormString(request.Password); err != nil {
		return FormRequest{}, err
	}
	if err := request.Device.validate(); err != nil {
		return FormRequest{}, err
	}
	values := formObject{
		"device":    fullDeviceFormValue(request.Device),
		"email":     formString(request.Email),
		"password":  formString(request.Password),
		"permanent": formBool(request.Permanent),
	}
	return buildFormRequest(RoutePasscodeGenerate, values)
}

// PasscodeRegisterRequest is the reviewed passcode device-registration body.
// Whether/when this operation follows generation is intentionally out of
// scope; this type only encodes the established request shape.
type PasscodeRegisterRequest struct {
	Email    string
	Password string
	Device   UUIDOnlyDevice
}

func BuildPasscodeRegisterRequest(request PasscodeRegisterRequest) (FormRequest, error) {
	if err := validateRequiredFormString(request.Email); err != nil {
		return FormRequest{}, err
	}
	if err := validateRequiredFormString(request.Password); err != nil {
		return FormRequest{}, err
	}
	if err := request.Device.validate(); err != nil {
		return FormRequest{}, err
	}
	return buildFormRequest(RoutePasscodeRegister, formObject{
		"device":   uuidOnlyDeviceFormValue(request.Device),
		"email":    formString(request.Email),
		"password": formString(request.Password),
	})
}

// PasscodeCancelRequest is the reviewed passcode cancellation body.
type PasscodeCancelRequest struct {
	Email    string
	Password string
	Device   UUIDOnlyDevice
}

func BuildPasscodeCancelRequest(request PasscodeCancelRequest) (FormRequest, error) {
	if err := validateRequiredFormString(request.Email); err != nil {
		return FormRequest{}, err
	}
	if err := validateRequiredFormString(request.Password); err != nil {
		return FormRequest{}, err
	}
	if err := request.Device.validate(); err != nil {
		return FormRequest{}, err
	}
	return buildFormRequest(RoutePasscodeCancel, formObject{
		"device":   uuidOnlyDeviceFormValue(request.Device),
		"email":    formString(request.Email),
		"password": formString(request.Password),
	})
}

// QRCancelRequest is the reviewed QR cancellation body.
type QRCancelRequest struct {
	ID     string
	Device UUIDOnlyDevice
}

func BuildQRCancelRequest(request QRCancelRequest) (FormRequest, error) {
	if err := validateRequiredFormString(request.ID); err != nil {
		return FormRequest{}, err
	}
	if err := request.Device.validate(); err != nil {
		return FormRequest{}, err
	}
	return buildFormRequest(RouteQRCancel, formObject{
		"device": uuidOnlyDeviceFormValue(request.Device),
		"id":     formString(request.ID),
	})
}

// QRLoginRequest is the reviewed QR approval-poll body. It does not decode or
// classify the corresponding response.
type QRLoginRequest struct {
	ID     string
	Device UUIDOnlyDevice
}

func BuildQRLoginRequest(request QRLoginRequest) (FormRequest, error) {
	if err := validateRequiredFormString(request.ID); err != nil {
		return FormRequest{}, err
	}
	if err := request.Device.validate(); err != nil {
		return FormRequest{}, err
	}
	return buildFormRequest(RouteQRLogin, formObject{
		"device": uuidOnlyDeviceFormValue(request.Device),
		"id":     formString(request.ID),
	})
}

// QRPasswordCheckRequest is the reviewed password-only QR-family body. Its
// ownership in the larger registration state machine remains unresolved.
type QRPasswordCheckRequest struct {
	Password string
}

func BuildQRPasswordCheckRequest(request QRPasswordCheckRequest) (FormRequest, error) {
	if err := validateRequiredFormString(request.Password); err != nil {
		return FormRequest{}, err
	}
	return buildFormRequest(RouteQRPasswordCheck, formObject{
		"password": formString(request.Password),
	})
}

func buildFormRequest(route Route, values formObject) (FormRequest, error) {
	profile, ok := ProfileFor(route)
	if !ok {
		return FormRequest{}, ErrInvalidFormRequest
	}
	body, err := encodeForm(values)
	if err != nil {
		return FormRequest{}, err
	}
	if len(body) > MaxFormBodyBytes {
		return FormRequest{}, ErrFormTooLarge
	}
	return FormRequest{
		Profile:     profile,
		ContentType: RegistrationFormContentType,
		Body:        body,
	}, nil
}

type formValue struct {
	text   string
	object formObject
	kind   formValueKind
}

type formValueKind uint8

const (
	formStringKind formValueKind = iota + 1
	formObjectKind
)

type formObject map[string]formValue

func formString(value string) formValue {
	return formValue{kind: formStringKind, text: value}
}

func formBool(value bool) formValue {
	if value {
		return formString("1")
	}
	return formString("0")
}

func fullDeviceFormValue(device FullDevice) formValue {
	return formValue{
		kind: formObjectKind,
		object: formObject{
			"model":     formString(device.Model),
			"name":      formString(device.Name),
			"osVersion": formString(device.OSVersion),
			"uuid":      formString(device.UUID),
		},
	}
}

func uuidOnlyDeviceFormValue(device UUIDOnlyDevice) formValue {
	return formValue{
		kind: formObjectKind,
		object: formObject{
			"uuid": formString(device.UUID),
		},
	}
}

// encodeForm mirrors the reviewed Alamofire dictionary traversal. Top-level
// keys are sorted as observed; nested keys are sorted here only to make the
// otherwise unspecified nested-dictionary order deterministic. Nested keys use
// bracket notation. Only strings are emitted by the typed builders above,
// avoiding reflection and accidental protocol guesses.
func encodeForm(values formObject) ([]byte, error) {
	components := make([]string, 0, len(values))
	if err := appendFormComponents(&components, "", values); err != nil {
		return nil, err
	}
	return []byte(strings.Join(components, "&")), nil
}

func appendFormComponents(components *[]string, prefix string, values formObject) error {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key == "" || !utf8.ValidString(key) {
			return ErrInvalidFormRequest
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := values[key]
		name := key
		if prefix != "" {
			name = prefix + "[" + key + "]"
		}
		switch value.kind {
		case formStringKind:
			if !validOptionalFormString(value.text) {
				return ErrInvalidFormRequest
			}
			*components = append(*components, percentEncode(name)+"="+percentEncode(value.text))
		case formObjectKind:
			if len(value.object) == 0 {
				return ErrInvalidFormRequest
			}
			if err := appendFormComponents(components, name, value.object); err != nil {
				return err
			}
		default:
			return ErrInvalidFormRequest
		}
	}
	return nil
}

func validOptionalFormString(value string) bool {
	return validateOptionalFormString(value) == nil
}

func validateRequiredFormString(value string) error {
	if value == "" {
		return ErrMissingFormField
	}
	if !utf8.ValidString(value) {
		return ErrInvalidFormRequest
	}
	if len(value) > MaxFormFieldBytes {
		return ErrFormTooLarge
	}
	return nil
}

func validateOptionalFormString(value string) error {
	if !utf8.ValidString(value) {
		return ErrInvalidFormRequest
	}
	if len(value) > MaxFormFieldBytes {
		return ErrFormTooLarge
	}
	return nil
}

// percentEncode follows Alamofire's afURLQueryAllowed character set. In
// particular, spaces become %20 (never '+'), '/', and '?' remain allowed, and
// brackets, '&', '=', '+', and '%' are escaped.
func percentEncode(value string) string {
	const hex = "0123456789ABCDEF"
	var builder strings.Builder
	for i := 0; i < len(value); i++ {
		b := value[i]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
			(b >= '0' && b <= '9') || b == '-' || b == '.' || b == '_' || b == '~' || b == '/' || b == '?' {
			builder.WriteByte(b)
			continue
		}
		builder.WriteByte('%')
		builder.WriteByte(hex[b>>4])
		builder.WriteByte(hex[b&0x0f])
	}
	return builder.String()
}
