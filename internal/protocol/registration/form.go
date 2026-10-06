package registration

import (
	"bytes"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

// RegistrationJSONContentType is the exact content type emitted by the
// reviewed Mac JSON request serializer.
const RegistrationJSONContentType = "application/json"

// RegistrationFormContentType is retained as a source-compatible alias while
// the request types are renamed in a later API cleanup.
const RegistrationFormContentType = RegistrationJSONContentType

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

func (r FormRequest) String() string {
	return "FormRequest{route=" + string(r.Profile.Route) +
		", method=" + string(r.Profile.Method) +
		", contentType=" + r.ContentType + ", body=<redacted>}"
}

func (r FormRequest) GoString() string { return r.String() }

// BuildQRGenerateRequest validates and JSON-encodes one QR generation request.
func BuildQRGenerateRequest(request QRGenerateRequest) (FormRequest, error) {
	if err := request.Device.validate(); err != nil {
		return FormRequest{}, err
	}
	if request.PreviousID != nil {
		if err := validateOptionalFormString(*request.PreviousID); err != nil {
			return FormRequest{}, err
		}
	}
	values := map[string]any{
		"device": fullDeviceJSONValue(request.Device),
	}
	if request.PreviousID != nil {
		values["previousId"] = *request.PreviousID
	}
	return buildFormRequest(RouteQRGenerate, values)
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
	return buildFormRequest(RouteQRCancel, map[string]any{
		"device": uuidOnlyDeviceJSONValue(request.Device),
		"id":     request.ID,
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
	return buildFormRequest(RouteQRLogin, map[string]any{
		"device": uuidOnlyDeviceJSONValue(request.Device),
		"id":     request.ID,
	})
}

func buildFormRequest(route Route, values map[string]any) (FormRequest, error) {
	profile, ok := ProfileFor(route)
	if !ok {
		return FormRequest{}, ErrInvalidFormRequest
	}
	body, err := marshalJSON(values)
	if err != nil {
		return FormRequest{}, err
	}
	if len(body) > MaxFormBodyBytes {
		return FormRequest{}, ErrFormTooLarge
	}
	return FormRequest{
		Profile:     profile,
		ContentType: RegistrationJSONContentType,
		Body:        body,
	}, nil
}

func marshalJSON(values map[string]any) ([]byte, error) {
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(values); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(body.Bytes(), []byte{'\n'}), nil
}

func fullDeviceJSONValue(device FullDevice) map[string]any {
	return map[string]any{
		"model":     device.Model,
		"name":      device.Name,
		"osVersion": device.OSVersion,
		"uuid":      device.UUID,
	}
}

func uuidOnlyDeviceJSONValue(device UUIDOnlyDevice) map[string]any {
	return map[string]any{
		"uuid": device.UUID,
	}
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
