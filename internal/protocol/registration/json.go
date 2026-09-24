package registration

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
)

// Registration JSON is decoded only from bounded in-memory bodies. This is a
// defensive limit and is not a claim about server-side response size.
const (
	MaxRegistrationJSONBytes = 64 * 1024
	MaxRegistrationJSONField = 16 * 1024
)

var (
	ErrJSONBodyTooLarge     = errors.New("registration: JSON body too large")
	ErrJSONFieldTooLarge    = errors.New("registration: JSON field too large")
	ErrInvalidJSONResponse  = errors.New("registration: invalid JSON response")
	ErrMissingJSONField     = errors.New("registration: missing JSON field")
	ErrWrongJSONType        = errors.New("registration: wrong JSON field type")
	ErrUnexpectedHTTPStatus = errors.New("registration: unexpected HTTP status")
	ErrQRLoginNotSuccessful = errors.New("registration: QR login was not successful")
	ErrInvalidQRDelay       = errors.New("registration: invalid QR delay")
)

// QRGenerateResponse is the reviewed QR-generation subset. RemainingSeconds
// stays as a server number; no wall-clock or time.Duration conversion occurs.
type QRGenerateResponse struct {
	Status           int64
	RemainingSeconds float64
	Presentation     QRPresentation
}

func (r QRGenerateResponse) String() string {
	return "QRGenerateResponse{status=" + strconv.FormatInt(r.Status, 10) +
		", url=<redacted>, remainingSeconds=" + strconv.FormatFloat(r.RemainingSeconds, 'g', -1, 64) + "}"
}

func (r QRGenerateResponse) GoString() string { return r.String() }

// DecodeQRGenerateResponse accepts only the evidenced HTTP 200 object shape
// and feeds the complete URL through ParseQRPresentation.
func DecodeQRGenerateResponse(httpStatus int, body []byte) (QRGenerateResponse, error) {
	if httpStatus != 200 {
		return QRGenerateResponse{}, ErrUnexpectedHTTPStatus
	}
	object, err := decodeJSONObject(body)
	if err != nil {
		return QRGenerateResponse{}, err
	}
	status, err := requiredInteger(object, "status")
	if err != nil {
		return QRGenerateResponse{}, err
	}
	payload, err := requiredString(object, "url")
	if err != nil {
		return QRGenerateResponse{}, err
	}
	remaining, err := requiredPositiveFiniteNumber(object, "remainingSeconds")
	if err != nil {
		return QRGenerateResponse{}, err
	}
	presentation, err := ParseQRPresentation(payload)
	if err != nil {
		return QRGenerateResponse{}, err
	}
	return QRGenerateResponse{
		Status:           status,
		RemainingSeconds: remaining,
		Presentation:     presentation,
	}, nil
}

// OptionalString preserves whether an observed string key was present without
// making it required. Its value is accessible only through Value, while its
// formatting methods are always redacted.
type OptionalString struct {
	value   string
	present bool
}

func (v OptionalString) Present() bool { return v.present }
func (v OptionalString) Value() (string, bool) {
	return v.value, v.present
}
func (v OptionalString) String() string   { return optionalMarker(v.present) }
func (v OptionalString) GoString() string { return v.String() }

// OptionalBool preserves explicit presence for the observed permanent field.
type OptionalBool struct {
	value   bool
	present bool
}

func (v OptionalBool) Present() bool { return v.present }
func (v OptionalBool) Value() (bool, bool) {
	return v.value, v.present
}
func (v OptionalBool) String() string   { return optionalMarker(v.present) }
func (v OptionalBool) GoString() string { return v.String() }

// OptionalInt64 preserves explicit presence for user.userId.
type OptionalInt64 struct {
	value   int64
	present bool
}

func (v OptionalInt64) Present() bool { return v.present }
func (v OptionalInt64) Value() (int64, bool) {
	return v.value, v.present
}
func (v OptionalInt64) String() string   { return optionalMarker(v.present) }
func (v OptionalInt64) GoString() string { return v.String() }

// QRLoginSuccess preserves the observed QR-login handoff fields without
// asserting optionality or installing credentials. UserPresent distinguishes
// an absent user object from an explicitly decoded object.
type QRLoginSuccess struct {
	Status             int64
	Permanent          OptionalBool
	UserPresent        bool
	UserID             OptionalInt64
	AccessToken        OptionalString
	RefreshToken       OptionalString
	TokenType          OptionalString
	AutoLoginAccountID OptionalString
	DisplayAccountID   OptionalString
}

// DecodeQRLoginSuccess accepts only HTTP 200 and integer status zero. Every
// observed non-status field is optional in this model because current evidence
// does not establish its requiredness; present fields must have their reviewed
// type. Unknown fields are ignored rather than guessed.
func DecodeQRLoginSuccess(httpStatus int, body []byte) (QRLoginSuccess, error) {
	if httpStatus != 200 {
		return QRLoginSuccess{}, ErrUnexpectedHTTPStatus
	}
	object, err := decodeJSONObject(body)
	if err != nil {
		return QRLoginSuccess{}, err
	}
	status, err := requiredInteger(object, "status")
	if err != nil {
		return QRLoginSuccess{}, err
	}
	if status != 0 {
		return QRLoginSuccess{}, ErrQRLoginNotSuccessful
	}
	result := QRLoginSuccess{Status: status}
	if raw, ok := object["permanent"]; ok {
		value, err := decodeBool(raw)
		if err != nil {
			return QRLoginSuccess{}, err
		}
		result.Permanent = OptionalBool{value: value, present: true}
	}
	if raw, ok := object["user"]; ok {
		user, err := decodeJSONObject(raw)
		if err != nil {
			return QRLoginSuccess{}, err
		}
		result.UserPresent = true
		if userID, present := user["userId"]; present {
			value, err := decodeInteger(userID)
			if err != nil {
				return QRLoginSuccess{}, err
			}
			result.UserID = OptionalInt64{value: value, present: true}
		}
	}
	for key, destination := range map[string]*OptionalString{
		"accessToken":        &result.AccessToken,
		"refreshToken":       &result.RefreshToken,
		"tokenType":          &result.TokenType,
		"autoLoginAccountId": &result.AutoLoginAccountID,
		"displayAccountId":   &result.DisplayAccountID,
	} {
		if raw, ok := object[key]; ok {
			value, err := decodeString(raw)
			if err != nil {
				return QRLoginSuccess{}, err
			}
			*destination = OptionalString{value: value, present: true}
		}
	}
	return result, nil
}

// MinimumFieldsPresent reports only the smallest handoff set justified by the
// current evidence. It does not install state, validate token contents, or
// require any unresolved optional field.
func (r QRLoginSuccess) MinimumFieldsPresent() bool {
	return r.UserID.Present() && r.AccessToken.Present()
}

func (r QRLoginSuccess) String() string {
	return "QRLoginSuccess{status=" + strconv.FormatInt(r.Status, 10) +
		", permanent=" + optionalMarker(r.Permanent.present) +
		", user=" + optionalMarker(r.UserPresent) +
		", userID=" + optionalMarker(r.UserID.present) +
		", accessToken=" + optionalMarker(r.AccessToken.present) +
		", refreshToken=" + optionalMarker(r.RefreshToken.present) +
		", tokenType=" + optionalMarker(r.TokenType.present) +
		", autoLoginAccountId=" + optionalMarker(r.AutoLoginAccountID.present) +
		", displayAccountId=" + optionalMarker(r.DisplayAccountID.present) + "}"
}

func (r QRLoginSuccess) GoString() string { return r.String() }

// RawJSONField retains bounded raw bytes and explicit presence while keeping
// formatting redacted. Callers that intentionally need the bytes receive a
// defensive copy from Raw.
type RawJSONField struct {
	raw     []byte
	present bool
}

func (f RawJSONField) Present() bool { return f.present }
func (f RawJSONField) Raw() []byte {
	if !f.present {
		return nil
	}
	return append([]byte(nil), f.raw...)
}
func (f RawJSONField) String() string   { return optionalMarker(f.present) }
func (f RawJSONField) GoString() string { return f.String() }

// ServerErrorEnvelope is the shared top-level error boundary. Only status is
// typed; reason, detailCode, and response remain bounded raw JSON because
// their exact types are unresolved.
type ServerErrorEnvelope struct {
	Status     int64
	Reason     RawJSONField
	DetailCode RawJSONField
	Response   RawJSONField
}

func DecodeServerErrorEnvelope(body []byte) (ServerErrorEnvelope, error) {
	object, err := decodeJSONObject(body)
	if err != nil {
		return ServerErrorEnvelope{}, err
	}
	status, err := requiredInteger(object, "status")
	if err != nil {
		return ServerErrorEnvelope{}, err
	}
	result := ServerErrorEnvelope{Status: status}
	for key, destination := range map[string]*RawJSONField{
		"reason":     &result.Reason,
		"detailCode": &result.DetailCode,
		"response":   &result.Response,
	} {
		if raw, ok := object[key]; ok {
			if len(raw) > MaxRegistrationJSONField {
				return ServerErrorEnvelope{}, ErrJSONFieldTooLarge
			}
			*destination = RawJSONField{raw: append([]byte(nil), raw...), present: true}
		}
	}
	return result, nil
}

// QROutcome maps a successfully extracted integer server status. Unknown
// values remain terminal unknown failures through the existing fail-closed
// policy mapping.
func (e ServerErrorEnvelope) QROutcome() Outcome {
	return DecodeQROutcome64(e.Status)
}

func (e ServerErrorEnvelope) String() string {
	return "ServerErrorEnvelope{status=" + strconv.FormatInt(e.Status, 10) +
		", reason=" + optionalMarker(e.Reason.present) +
		", detailCode=" + optionalMarker(e.DetailCode.present) +
		", response=" + optionalMarker(e.Response.present) + "}"
}

func (e ServerErrorEnvelope) GoString() string { return e.String() }

func decodeJSONObject(body []byte) (map[string]json.RawMessage, error) {
	if len(body) == 0 {
		return nil, ErrInvalidJSONResponse
	}
	if len(body) > MaxRegistrationJSONBytes {
		return nil, ErrJSONBodyTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, ErrInvalidJSONResponse
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, ErrInvalidJSONResponse
	}
	return object, nil
}

func requiredInteger(object map[string]json.RawMessage, key string) (int64, error) {
	raw, ok := object[key]
	if !ok {
		return 0, ErrMissingJSONField
	}
	return decodeInteger(raw)
}

func requiredString(object map[string]json.RawMessage, key string) (string, error) {
	raw, ok := object[key]
	if !ok {
		return "", ErrMissingJSONField
	}
	return decodeString(raw)
}

func requiredPositiveFiniteNumber(object map[string]json.RawMessage, key string) (float64, error) {
	raw, ok := object[key]
	if !ok {
		return 0, ErrMissingJSONField
	}
	value, err := decodeFiniteNumber(raw)
	if err != nil || value <= 0 {
		return 0, ErrInvalidQRDelay
	}
	return value, nil
}

func decodeInteger(raw json.RawMessage) (int64, error) {
	text := strings.TrimSpace(string(raw))
	if !json.Valid(raw) || text == "" || strings.ContainsAny(text, ".eE") {
		return 0, ErrWrongJSONType
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, ErrWrongJSONType
	}
	return value, nil
}

func decodeFiniteNumber(raw json.RawMessage) (float64, error) {
	text := strings.TrimSpace(string(raw))
	if !json.Valid(raw) || text == "" || text == "null" || text == "true" || text == "false" || strings.HasPrefix(text, "\"") {
		return 0, ErrWrongJSONType
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, ErrWrongJSONType
	}
	return value, nil
}

func decodeString(raw json.RawMessage) (string, error) {
	if len(raw) > MaxRegistrationJSONField {
		return "", ErrJSONFieldTooLarge
	}
	text := strings.TrimSpace(string(raw))
	if text == "" || text[0] != '"' || !json.Valid(raw) {
		return "", ErrWrongJSONType
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", ErrWrongJSONType
	}
	return value, nil
}

func decodeBool(raw json.RawMessage) (bool, error) {
	text := strings.TrimSpace(string(raw))
	switch text {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, ErrWrongJSONType
	}
}

func optionalMarker(present bool) string {
	if present {
		return "<present-redacted>"
	}
	return "<absent>"
}
