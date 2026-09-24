package registration

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
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
// It joins the reviewed base URL and route, uses the exact form body, applies
// only the evidenced Content-Type header, and leaves clients, cookies,
// authentication, signing, retries, and deadlines to the caller.
func NewHTTPRequest(ctx context.Context, form FormRequest) (*http.Request, error) {
	if ctx == nil {
		return nil, ErrNilRequestContext
	}
	expected, ok := ProfileFor(form.Profile.Route)
	if !ok || form.Profile != expected || form.ContentType != RegistrationFormContentType {
		return nil, ErrInvalidHTTPProfile
	}
	if len(form.Body) > MaxFormBodyBytes {
		return nil, ErrFormTooLarge
	}
	base, err := url.Parse(form.Profile.BaseURL)
	if err != nil || base.Scheme == "" || base.Host == "" || base.RawQuery != "" || base.Fragment != "" {
		return nil, ErrInvalidHTTPProfile
	}
	base.Path = strings.TrimRight(base.Path, "/") + string(form.Profile.Route)
	base.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, string(form.Profile.Method), base.String(), bytes.NewReader(form.Body))
	if err != nil {
		return nil, ErrInvalidHTTPProfile
	}
	req.Header.Set("Content-Type", form.ContentType)
	return req, nil
}
