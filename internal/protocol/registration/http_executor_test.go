package registration

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeHTTPDoer struct {
	calls   int
	request *http.Request
	fn      func(*http.Request) (*http.Response, error)
}

func (d *fakeHTTPDoer) Do(request *http.Request) (*http.Response, error) {
	d.calls++
	d.request = request
	return d.fn(request)
}

type trackingBody struct {
	reader   io.Reader
	closed   bool
	closeErr error
}

func (b *trackingBody) Read(p []byte) (int, error) { return b.reader.Read(p) }
func (b *trackingBody) Close() error {
	b.closed = true
	return b.closeErr
}

type failingReader struct {
	data []byte
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, r.err
}

type boundedReader struct {
	remaining int
}

type contextMarkerKey struct{}

func (r *boundedReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > r.remaining {
		n = r.remaining
	}
	for i := 0; i < n; i++ {
		p[i] = 'x'
	}
	r.remaining -= n
	return n, nil
}

func TestHTTPExecutorExecutesExactlyOnceAndReturnsStatusBodyOnly(t *testing.T) {
	form, err := BuildQRPasswordCheckRequest(QRPasswordCheckRequest{Password: "synthetic password"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), contextMarkerKey{}, "marker")
	responseBody := &trackingBody{reader: strings.NewReader(`{"status":0}`)}
	doer := &fakeHTTPDoer{fn: func(request *http.Request) (*http.Response, error) {
		if request.Context() != ctx {
			t.Fatal("executor did not preserve request context")
		}
		if request.Method != "POST" || request.Header.Get("Content-Type") != RegistrationFormContentType {
			t.Fatalf("request = %s headers=%#v", request.Method, request.Header)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != string(form.Body) {
			t.Fatalf("request body = %q, want %q", body, form.Body)
		}
		return &http.Response{StatusCode: 500, Header: http.Header{"Set-Cookie": []string{"must-not-escape"}}, Body: responseBody}, nil
	}}
	executor, err := NewHTTPExecutor(doer)
	if err != nil {
		t.Fatal(err)
	}
	response, err := executor.ExecuteForm(ctx, form)
	if err != nil {
		t.Fatal(err)
	}
	if doer.calls != 1 || !responseBody.closed {
		t.Fatalf("calls=%d closed=%v", doer.calls, responseBody.closed)
	}
	if response.StatusCode != 500 || string(response.Body) != `{"status":0}` {
		t.Fatalf("response = %#v", response)
	}
	response.Body[0] = 'X'
	if responseBody.closed == false {
		t.Fatal("body was not closed")
	}
	if strings.Contains(response.String(), `{"status":0}`) {
		t.Fatal("response String leaked body")
	}
}

func TestHTTPExecutorBodyReadSizeAndCloseFailures(t *testing.T) {
	tests := []struct {
		name string
		body io.Reader
		want error
	}{
		{name: "read failure", body: &failingReader{data: []byte("partial"), err: errors.New("reader marker")}, want: ErrHTTPBodyRead},
		{name: "oversized", body: &boundedReader{remaining: MaxRegistrationJSONBytes + 100}, want: ErrHTTPResponseTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &trackingBody{reader: test.body}
			doer := &fakeHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: body}, nil
			}}
			executor, err := NewHTTPExecutor(doer)
			if err != nil {
				t.Fatal(err)
			}
			request, err := http.NewRequestWithContext(context.Background(), "POST", "https://synthetic.invalid", nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = executor.Execute(request)
			if !errors.Is(err, test.want) || err.Error() != test.want.Error() {
				t.Fatalf("error = %v, want static %v", err, test.want)
			}
			if doer.calls != 1 || !body.closed {
				t.Fatalf("calls=%d closed=%v", doer.calls, body.closed)
			}
		})
	}
	closeBody := &trackingBody{reader: bytes.NewReader([]byte("{}")), closeErr: errors.New("closer marker")}
	doer := &fakeHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: closeBody}, nil
	}}
	executor, err := NewHTTPExecutor(doer)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequestWithContext(context.Background(), "POST", "https://synthetic.invalid", nil)
	if _, err := executor.Execute(request); !errors.Is(err, ErrHTTPBodyClose) {
		t.Fatalf("close error = %v", err)
	}
	if !closeBody.closed {
		t.Fatal("close failure body was not closed")
	}
}

func TestHTTPExecutorDistinguishesContextAndTransportFailuresWithoutLeakage(t *testing.T) {
	tests := []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
		want error
	}{
		{name: "canceled", ctx: func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, cancel
		}, want: ErrContextCanceled},
		{name: "deadline", ctx: func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithTimeout(context.Background(), 0)
			return ctx, cancel
		}, want: ErrContextDeadline},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doer := &fakeHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
				t.Fatal("Do called for an already-finished context")
				return nil, nil
			}}
			executor, err := NewHTTPExecutor(doer)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := test.ctx()
			defer cancel()
			request, _ := http.NewRequestWithContext(ctx, "POST", "https://example.invalid/path", nil)
			_, err = executor.Execute(request)
			if !errors.Is(err, test.want) || doer.calls != 0 {
				t.Fatalf("error=%v calls=%d, want %v/0", err, doer.calls, test.want)
			}
		})
	}
	marker := "https://example.invalid/body=redaction-marker"
	doer := &fakeHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
		return nil, errors.New(marker)
	}}
	executor, err := NewHTTPExecutor(doer)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequestWithContext(context.Background(), "POST", marker, strings.NewReader(marker))
	_, err = executor.Execute(request)
	if !errors.Is(err, ErrHTTPTransport) || err.Error() != ErrHTTPTransport.Error() || strings.Contains(err.Error(), marker) || doer.calls != 1 {
		t.Fatalf("transport error=%v calls=%d", err, doer.calls)
	}
}

func TestHTTPExecutorMapsDoerCancellationAndNilInputs(t *testing.T) {
	doer, err := NewHTTPExecutor(nil)
	if !errors.Is(err, ErrNilHTTPDoer) || doer != nil {
		t.Fatalf("nil doer = %#v/%v", doer, err)
	}
	executor, _ := NewHTTPExecutor(&fakeHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
		return nil, context.Canceled
	}})
	request, _ := http.NewRequestWithContext(context.Background(), "POST", "https://synthetic.invalid", nil)
	if _, err := executor.Execute(nil); !errors.Is(err, ErrNilHTTPRequest) {
		t.Fatalf("nil request error = %v", err)
	}
	if _, err := executor.Execute(request); !errors.Is(err, ErrContextCanceled) {
		t.Fatalf("uncoupled doer cancellation error = %v", err)
	}
}
