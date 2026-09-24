package registration

import (
	"context"
	"errors"
	"strconv"
)

var (
	ErrNilQRServiceExecutor  = errors.New("registration: nil QR service executor")
	ErrNilQRPresentationGate = errors.New("registration: nil QR presentation validator")
	// ErrQRPresentationRejected is intentionally static: validator errors may
	// contain payload or check-key details and are never returned verbatim.
	ErrQRPresentationRejected = errors.New("registration: QR presentation rejected")
)

// QRPresentationValidator is the required check-key/presentation gate. There
// is intentionally no permissive default; a service cannot produce a
// presentable challenge without an injected validator.
type QRPresentationValidator interface {
	Validate(QRPresentation) error
}

// QRRegistrationService composes reviewed form builders, an injected
// single-attempt executor, and the reviewed QR response codecs. It does not
// create an HTTP client, retry, install credentials, or run the reducer.
type QRRegistrationService struct {
	executor  *HTTPExecutor
	validator QRPresentationValidator
}

func NewQRRegistrationService(executor *HTTPExecutor, validator QRPresentationValidator) (*QRRegistrationService, error) {
	if executor == nil {
		return nil, ErrNilQRServiceExecutor
	}
	if validator == nil {
		return nil, ErrNilQRPresentationGate
	}
	return &QRRegistrationService{executor: executor, validator: validator}, nil
}

// QRChallenge is returned only after the injected presentation validator
// accepts the parsed server payload. RemainingSeconds is preserved as the
// server number and is not converted using wall-clock state.
type QRChallenge struct {
	Status           int64
	RemainingSeconds float64
	Presentation     QRPresentation
}

func (c QRChallenge) String() string {
	return "QRChallenge{status=" + strconv.FormatInt(c.Status, 10) +
		", remainingSeconds=" + strconv.FormatFloat(c.RemainingSeconds, 'g', -1, 64) +
		", presentation=<redacted>}"
}

func (c QRChallenge) GoString() string { return c.String() }

// Generate builds and executes one QR-generation request, decodes its bounded
// response, and applies the mandatory presentation/check-key gate before any
// challenge is returned.
func (s *QRRegistrationService) Generate(ctx context.Context, request QRGenerateRequest) (QRChallenge, error) {
	if s == nil || s.executor == nil {
		return QRChallenge{}, ErrNilQRServiceExecutor
	}
	if s.validator == nil {
		return QRChallenge{}, ErrNilQRPresentationGate
	}
	form, err := BuildQRGenerateRequest(request)
	if err != nil {
		return QRChallenge{}, err
	}
	response, err := s.executor.ExecuteForm(ctx, form)
	if err != nil {
		return QRChallenge{}, err
	}
	decoded, err := DecodeQRGenerateResponse(response.StatusCode, response.Body)
	if err != nil {
		return QRChallenge{}, err
	}
	if err := s.validator.Validate(decoded.Presentation); err != nil {
		return QRChallenge{}, ErrQRPresentationRejected
	}
	return QRChallenge(decoded), nil
}

// QRPollResultKind identifies the two response envelopes admitted by the
// polling seam. Success and ServerError are mutually exclusive.
type QRPollResultKind uint8

const (
	QRPollSuccess QRPollResultKind = iota + 1
	QRPollServerError
)

// QRPollResult preserves HTTP status and the typed body branch without
// exposing headers/cookies or formatting secrets. ServerError retains only the
// bounded raw fields established by the shared error envelope.
type QRPollResult struct {
	HTTPStatus  int
	Kind        QRPollResultKind
	Success     *QRLoginSuccess
	ServerError *ServerErrorEnvelope
}

func (r QRPollResult) String() string {
	return "QRPollResult{httpStatus=" + strconv.Itoa(r.HTTPStatus) +
		", kind=" + strconv.FormatUint(uint64(r.Kind), 10) +
		", body=<redacted>}"
}

func (r QRPollResult) GoString() string { return r.String() }

// Poll builds and executes one QR-login poll. HTTP 200/status 0 is decoded as
// success; a nonzero integer status or non-200 response is decoded as the
// shared server-error envelope. No retry or outcome-to-reducer mapping occurs.
func (s *QRRegistrationService) Poll(ctx context.Context, request QRLoginRequest) (QRPollResult, error) {
	if s == nil || s.executor == nil {
		return QRPollResult{}, ErrNilQRServiceExecutor
	}
	form, err := BuildQRLoginRequest(request)
	if err != nil {
		return QRPollResult{}, err
	}
	response, err := s.executor.ExecuteForm(ctx, form)
	if err != nil {
		return QRPollResult{}, err
	}
	success, successErr := DecodeQRLoginSuccess(response.StatusCode, response.Body)
	if successErr == nil {
		return QRPollResult{
			HTTPStatus: response.StatusCode,
			Kind:       QRPollSuccess,
			Success:    &success,
		}, nil
	}
	if response.StatusCode == 200 {
		object, objectErr := decodeJSONObject(response.Body)
		if objectErr != nil {
			return QRPollResult{}, successErr
		}
		status, statusErr := requiredInteger(object, "status")
		if statusErr != nil || status == 0 {
			return QRPollResult{}, successErr
		}
	}
	serverError, serverErr := DecodeServerErrorEnvelope(response.Body)
	if serverErr == nil {
		return QRPollResult{
			HTTPStatus:  response.StatusCode,
			Kind:        QRPollServerError,
			ServerError: &serverError,
		}, nil
	}
	// Preserve the more specific success-decoder failure for malformed
	// success-shaped bodies; neither decoder's values are included in errors.
	if response.StatusCode == 200 && !errors.Is(successErr, ErrQRLoginNotSuccessful) {
		return QRPollResult{}, successErr
	}
	return QRPollResult{}, serverErr
}
