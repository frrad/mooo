package registration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type qrServiceContextKey struct{}

type fakeQRValidator struct {
	calls int
	got   QRPresentation
	err   error
}

func (v *fakeQRValidator) Validate(presentation QRPresentation) error {
	v.calls++
	v.got = presentation
	return v.err
}

func newQRServiceForTest(t *testing.T, status int, body string, validator QRPresentationValidator) (*QRRegistrationService, *fakeHTTPDoer) {
	t.Helper()
	doer := &fakeHTTPDoer{fn: func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
	}}
	executor, err := NewHTTPExecutor(doer)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewQRRegistrationService(executor, validator)
	if err != nil {
		t.Fatal(err)
	}
	return service, doer
}

func syntheticQRRequest() QRGenerateRequest {
	return QRGenerateRequest{
		Device: FullDevice{Name: "synthetic", UUID: "uuid", OSVersion: "mac", Model: "model"},
	}
}

func syntheticQRLoginRequest() QRLoginRequest {
	return QRLoginRequest{ID: "synthetic-id", Device: UUIDOnlyDevice{UUID: "uuid"}}
}

func TestQRServiceGenerateRequiresValidatorAndReturnsOnlyValidatedChallenge(t *testing.T) {
	validator := &fakeQRValidator{}
	service, doer := newQRServiceForTest(t, 200, `{"status":0,"url":"synthetic://host?id=challenge","remainingSeconds":3.5}`, validator)
	challenge, err := service.Generate(context.Background(), syntheticQRRequest())
	if err != nil {
		t.Fatal(err)
	}
	if doer.calls != 1 || validator.calls != 1 || validator.got.ID != "challenge" {
		t.Fatalf("calls=%d validator=%d got=%#v", doer.calls, validator.calls, validator.got)
	}
	if challenge.Presentation.ID != "challenge" || challenge.RemainingSeconds != 3.5 || challenge.Status != 0 {
		t.Fatalf("challenge = %#v", challenge)
	}
	if strings.Contains(fmt.Sprintf("%v", challenge), "synthetic://host") || strings.Contains(fmt.Sprintf("%#v", challenge), "synthetic://host") {
		t.Fatal("challenge formatting leaked payload")
	}

	rejecting := &fakeQRValidator{err: errors.New("secret check-key details")}
	service, doer = newQRServiceForTest(t, 200, `{"status":0,"url":"synthetic://host?id=secret-id","remainingSeconds":3}`, rejecting)
	_, err = service.Generate(context.Background(), syntheticQRRequest())
	if !errors.Is(err, ErrQRPresentationRejected) || err.Error() != ErrQRPresentationRejected.Error() || strings.Contains(err.Error(), "secret") || doer.calls != 1 {
		t.Fatalf("validator rejection = %v calls=%d", err, doer.calls)
	}
}

func TestQRServiceGenerateComposesReviewedRequestAndPreservesContext(t *testing.T) {
	validator := &fakeQRValidator{}
	ctx := context.WithValue(context.Background(), qrServiceContextKey{}, "service-context")
	var calls int
	doer := &fakeHTTPDoer{fn: func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Context() != ctx || request.Method != "POST" || request.URL.String() != "https://katalk.kakao.com/mac/account/qrCodeLogin/generate" {
			t.Fatalf("request = %s %s context=%v", request.Method, request.URL, request.Context())
		}
		if len(request.Header) != 1 || request.Header.Get("Content-Type") != RegistrationFormContentType {
			t.Fatalf("headers = %#v", request.Header)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		want := "device%5Bmodel%5D=model&device%5Bname%5D=synthetic&device%5BosVersion%5D=mac&device%5Buuid%5D=uuid"
		if string(body) != want {
			t.Fatalf("body = %q, want %q", body, want)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":0,"url":"synthetic://host?id=challenge","remainingSeconds":3}`))}, nil
	}}
	executor, err := NewHTTPExecutor(doer)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewQRRegistrationService(executor, validator)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Generate(ctx, syntheticQRRequest()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || validator.calls != 1 {
		t.Fatalf("calls=%d validator=%d", calls, validator.calls)
	}
}

func TestQRServiceGenerateRequiresInjectedValidator(t *testing.T) {
	doer := &fakeHTTPDoer{fn: func(*http.Request) (*http.Response, error) { panic("should not execute") }}
	executor, err := NewHTTPExecutor(doer)
	if err != nil {
		t.Fatal(err)
	}
	if service, err := NewQRRegistrationService(executor, nil); !errors.Is(err, ErrNilQRPresentationGate) || service != nil {
		t.Fatalf("nil validator constructor = %#v/%v", service, err)
	}
}

func TestQRServicePollReturnsExplicitSuccessOrServerError(t *testing.T) {
	validator := &fakeQRValidator{}
	service, doer := newQRServiceForTest(t, 200, `{"status":0,"user":{"userId":7},"accessToken":"synthetic-access-value"}`, validator)
	result, err := service.Poll(context.Background(), syntheticQRLoginRequest())
	if err != nil {
		t.Fatal(err)
	}
	if doer.calls != 1 || result.Kind != QRPollSuccess || result.HTTPStatus != 200 || result.Success == nil || result.ServerError != nil || !result.Success.MinimumFieldsPresent() {
		t.Fatalf("success result = %#v calls=%d", result, doer.calls)
	}
	if strings.Contains(fmt.Sprintf("%v", result), "synthetic-access-value") || strings.Contains(fmt.Sprintf("%#v", result), "synthetic-access-value") {
		t.Fatal("poll result formatting leaked token")
	}

	service, doer = newQRServiceForTest(t, 200, `{"status":14,"nextRequestIntervalInSeconds":3}`, validator)
	result, err = service.Poll(context.Background(), syntheticQRLoginRequest())
	if err != nil {
		t.Fatal(err)
	}
	if doer.calls != 1 || result.Kind != QRPollServerError || result.HTTPStatus != 200 || result.ServerError == nil || result.Success != nil || result.ServerError.Status != 14 || result.ServerError.QROutcome() != OutcomePending {
		t.Fatalf("pending result = %#v calls=%d", result, doer.calls)
	}

	service, _ = newQRServiceForTest(t, 403, `{"status":20,"reason":"restricted"}`, validator)
	result, err = service.Poll(context.Background(), syntheticQRLoginRequest())
	if err != nil || result.Kind != QRPollServerError || result.HTTPStatus != 403 || result.ServerError == nil || result.ServerError.QROutcome() != OutcomeRestricted {
		t.Fatalf("restricted result = %#v err=%v", result, err)
	}
}

func TestQRServicePollFailsClosedWithoutRetryOnMalformedBody(t *testing.T) {
	service, doer := newQRServiceForTest(t, 200, `{"status":0,"user":{"userId":1.5}}`, &fakeQRValidator{})
	_, err := service.Poll(context.Background(), syntheticQRLoginRequest())
	if !errors.Is(err, ErrWrongJSONType) || doer.calls != 1 || strings.Contains(err.Error(), "1.5") {
		t.Fatalf("malformed poll error=%v calls=%d", err, doer.calls)
	}

	service, doer = newQRServiceForTest(t, 200, `{"status":14`, &fakeQRValidator{})
	_, err = service.Poll(context.Background(), syntheticQRLoginRequest())
	if !errors.Is(err, ErrInvalidJSONResponse) || doer.calls != 1 {
		t.Fatalf("invalid poll error=%v calls=%d", err, doer.calls)
	}
}

func TestQRServicePropagatesTransportFailureWithoutLeakingValues(t *testing.T) {
	secret := "https://synthetic.invalid/qr?token=synthetic-value"
	doer := &fakeHTTPDoer{fn: func(*http.Request) (*http.Response, error) {
		return nil, errors.New(secret)
	}}
	executor, err := NewHTTPExecutor(doer)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewQRRegistrationService(executor, &fakeQRValidator{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Generate(context.Background(), syntheticQRRequest())
	if !errors.Is(err, ErrHTTPTransport) || err.Error() != ErrHTTPTransport.Error() || strings.Contains(err.Error(), secret) || doer.calls != 1 {
		t.Fatalf("transport error=%v calls=%d", err, doer.calls)
	}
}
