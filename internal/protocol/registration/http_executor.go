package registration

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
)

var (
	ErrNilHTTPDoer          = errors.New("registration: nil HTTP doer")
	ErrNilHTTPRequest       = errors.New("registration: nil HTTP request")
	ErrContextCanceled      = errors.New("registration: request context canceled")
	ErrContextDeadline      = errors.New("registration: request context deadline exceeded")
	ErrHTTPTransport        = errors.New("registration: HTTP transport failure")
	ErrHTTPBodyRead         = errors.New("registration: HTTP body read failure")
	ErrHTTPBodyClose        = errors.New("registration: HTTP body close failure")
	ErrHTTPResponseTooLarge = errors.New("registration: HTTP response body too large")
)

// Doer is the only execution dependency. A caller may supply a custom test
// executor, transport wrapper, or deliberately configured client; this package
// never creates a default client or changes redirects/cookies/retries.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// HTTPDoer is an explanatory alias for callers that prefer the longer name.
type HTTPDoer = Doer

// HTTPExecutor performs one injected HTTP attempt. It has no retry loop,
// redirect policy, cookie store, auth behavior, or response-header output.
type HTTPExecutor struct {
	doer Doer
}

func NewHTTPExecutor(doer Doer) (*HTTPExecutor, error) {
	if doer == nil {
		return nil, ErrNilHTTPDoer
	}
	return &HTTPExecutor{doer: doer}, nil
}

// HTTPResponse contains only the status and a defensive response-body copy.
// Headers and cookies deliberately do not cross this boundary.
type HTTPResponse struct {
	StatusCode int
	Body       []byte
}

func (r HTTPResponse) String() string {
	return "HTTPResponse{status=" + strconv.Itoa(r.StatusCode) + ", body=<redacted>}"
}

func (r HTTPResponse) GoString() string { return r.String() }

// Execute performs exactly one Doer call and consumes/closes its response
// body. Context cancellation/deadline, transport, read, close, and size
// failures are returned as static redacted sentinel errors.
func (e *HTTPExecutor) Execute(request *http.Request) (HTTPResponse, error) {
	if e == nil || e.doer == nil {
		return HTTPResponse{}, ErrNilHTTPDoer
	}
	if request == nil {
		return HTTPResponse{}, ErrNilHTTPRequest
	}
	if err := contextState(request.Context()); err != nil {
		return HTTPResponse{}, err
	}
	response, err := e.doer.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if contextErr := contextState(request.Context()); contextErr != nil {
			return HTTPResponse{}, contextErr
		}
		if errors.Is(err, context.Canceled) {
			return HTTPResponse{}, ErrContextCanceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return HTTPResponse{}, ErrContextDeadline
		}
		return HTTPResponse{}, ErrHTTPTransport
	}
	if response == nil || response.Body == nil {
		return HTTPResponse{}, ErrHTTPTransport
	}
	return readHTTPResponse(request, response)
}

// ExecuteForm constructs the reviewed request and executes it once.
func (e *HTTPExecutor) ExecuteForm(ctx context.Context, form FormRequest) (HTTPResponse, error) {
	request, err := NewHTTPRequest(ctx, form)
	if err != nil {
		return HTTPResponse{}, err
	}
	return e.Execute(request)
}

func readHTTPResponse(request *http.Request, response *http.Response) (result HTTPResponse, err error) {
	defer func() {
		closeErr := response.Body.Close()
		if err == nil && closeErr != nil {
			result = HTTPResponse{}
			err = ErrHTTPBodyClose
		}
	}()
	limited := io.LimitReader(response.Body, int64(MaxRegistrationJSONBytes)+1)
	body, readErr := io.ReadAll(limited)
	if readErr != nil {
		if contextErr := contextState(request.Context()); contextErr != nil {
			return HTTPResponse{}, contextErr
		}
		return HTTPResponse{}, ErrHTTPBodyRead
	}
	if len(body) > MaxRegistrationJSONBytes {
		return HTTPResponse{}, ErrHTTPResponseTooLarge
	}
	return HTTPResponse{StatusCode: response.StatusCode, Body: append([]byte(nil), body...)}, nil
}

func contextState(ctx context.Context) error {
	if ctx == nil {
		return ErrContextCanceled
	}
	switch ctx.Err() {
	case context.Canceled:
		return ErrContextCanceled
	case context.DeadlineExceeded:
		return ErrContextDeadline
	default:
		return nil
	}
}
