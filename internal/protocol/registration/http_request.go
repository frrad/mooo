package registration

import (
	"bytes"
	"context"
	"errors"
	"net/http"
)

var (
	// ErrInvalidHTTPProfile is static and contains no body, URL, or credential
	// values.
	ErrInvalidHTTPProfile = errors.New("registration: invalid HTTP profile")
	// ErrNilRequestContext is returned before calling net/http with a nil
	// context so construction remains deterministic and panic-free.
	ErrNilRequestContext = errors.New("registration: nil request context")
)

// NewHTTPRequest constructs, but never executes, one registration request.
// It POSTs the exact JSON body to the reviewed base URL and route, applies
// only the evidenced Content-Type header, and leaves clients, cookies,
// authentication, signing, retries, and deadlines to the caller.
func NewHTTPRequest(ctx context.Context, form FormRequest) (*http.Request, error) {
	if ctx == nil {
		return nil, ErrNilRequestContext
	}
	if !knownRoute(form.Route) || form.ContentType != RegistrationJSONContentType {
		return nil, ErrInvalidHTTPProfile
	}
	if len(form.Body) > MaxFormBodyBytes {
		return nil, ErrFormTooLarge
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, RegistrationBaseURL+string(form.Route), bytes.NewReader(form.Body))
	if err != nil {
		return nil, ErrInvalidHTTPProfile
	}
	req.Header.Set("Content-Type", form.ContentType)
	return req, nil
}
