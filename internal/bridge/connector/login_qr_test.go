package connector

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/registration"
)

type fakeQRBackend struct {
	mu           sync.Mutex
	challenge    registration.QRChallenge
	poll         qrPollResult
	cancels      int
	pollStarted  chan struct{}
	pollFn       func(context.Context) (qrPollResult, error)
	pollCalls    int
	cancelFn     func(context.Context) error
	pollSequence []qrPollResult
}

func (f *fakeQRBackend) Generate(context.Context, registration.QRGenerateRequest) (registration.QRChallenge, error) {
	return f.challenge, nil
}

func (f *fakeQRBackend) Poll(ctx context.Context, _ registration.QRLoginRequest) (qrPollResult, error) {
	f.mu.Lock()
	f.pollCalls++
	if f.pollStarted != nil {
		close(f.pollStarted)
		f.pollStarted = nil
	}
	f.mu.Unlock()
	if f.pollFn != nil {
		return f.pollFn(ctx)
	}
	if len(f.pollSequence) > 0 {
		result := f.pollSequence[0]
		f.pollSequence = f.pollSequence[1:]
		return result, nil
	}
	return f.poll, nil
}
func (f *fakeQRBackend) Cancel(ctx context.Context, _ registration.QRCancelRequest) error {
	return f.cancel(ctx)
}

func (f *fakeQRBackend) cancel(ctx context.Context) error {
	f.mu.Lock()
	f.cancels++
	cancelFn := f.cancelFn
	f.mu.Unlock()
	if cancelFn != nil {
		return cancelFn(ctx)
	}
	return nil
}

func newFakeQRBackend(t *testing.T, body string) *fakeQRBackend {
	t.Helper()
	presentation, err := registration.ParseQRPresentation("https://katalk.kakao.com/talk/account/qrCodeLogin/info.json?id=synthetic-id")
	if err != nil {
		t.Fatal(err)
	}
	success, err := registration.DecodeQRLoginSuccess(200, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return &fakeQRBackend{
		challenge: registration.QRChallenge{Status: 0, RemainingSeconds: 60, Presentation: presentation},
		poll:      qrPollResult{Result: registration.QRPollResult{HTTPStatus: 200, Kind: registration.QRPollSuccess, Success: &success}},
	}
}

func TestMacQRPresentationValidatorAcceptsObservedRelativePayload(t *testing.T) {
	presentation, err := registration.ParseQRPresentation("/talk/account/qrCodeLogin/info.json?id=a+b")
	if err != nil {
		t.Fatal(err)
	}
	if err := (macQRPresentationValidator{}).Validate(presentation); err != nil {
		t.Fatalf("relative QR payload rejected: %v", err)
	}
	if err := (macQRPresentationValidator{}).Validate(registration.QRPresentation{Payload: "https://katalk.kakao.com/talk/account/qrCodeLogin/info.json?id=a+b", ID: "a+b"}); err != nil {
		t.Fatalf("synthetic HTTPS QR payload rejected: %v", err)
	}
}

func TestQRDisplayStepPreservesServerPayloadForFrameworkRenderer(t *testing.T) {
	// The official macOS client supplies the complete server string directly to
	// its QR renderer. In particular, a relative account-info URL must remain
	// relative: the Mac host is unresolved and Android extracts the id from the
	// path without requiring a scheme or host. The bridgev2 framework owns PNG
	// rendering, so this assertion prevents a connector-side "normalization"
	// from silently changing the server-issued challenge.
	payload := "/talk/account/qrCodeLogin/info.json?id=a+b"
	step := qrDisplayStep(payload)
	if step == nil || step.DisplayAndWaitParams == nil {
		t.Fatal("QR display step is missing display parameters")
	}
	if got := step.DisplayAndWaitParams.Data; got != payload {
		t.Fatalf("QR renderer payload = %q, want original server payload %q", got, payload)
	}
}

func TestMacQRPresentationValidatorMalformedPayloadDoesNotPanic(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("validator panicked: %v", recovered)
		}
	}()
	if err := (macQRPresentationValidator{}).Validate(registration.QRPresentation{Payload: "http://[::1", ID: "id"}); err == nil {
		t.Fatal("malformed QR payload accepted")
	}
}

func TestMacHeaderDoerUsesReviewedHeadersAndDoesNotFollowRedirects(t *testing.T) {
	var seen *http.Request
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			seen = req
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://elsewhere.invalid/"}}, Body: io.NopCloser(strings.NewReader("redirect")), Request: req}, nil
		}),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	doer := macHeaderDoer{client: client, profile: registration.MacClientProfile{AppVersion: "26.8.0", OSVersion: "26.6.2", Language: "en"}}
	request, err := http.NewRequest(http.MethodPost, "https://katalk.kakao.com/mac/account/qrCodeLogin/cancel", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := doer.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusFound || seen == nil {
		t.Fatalf("response = %#v request = %#v", response, seen)
	}
	if seen.Header.Get("A") != "mac/26.8.0/en" || seen.Header.Get("User-Agent") != "KT/26.8.0 Mc/26.6.2 en" || seen.Header.Get("Accept-Language") != "en" || seen.Header.Get("Content-Type") != registration.RegistrationJSONContentType {
		t.Fatalf("headers = %#v", seen.Header)
	}
}

func TestQRServiceBackendCancelRejectsHTTP200ErrorEnvelope(t *testing.T) {
	responses := []string{`{"status":1}`, `{"status":0}`}
	doer := staticBodyDoer{responses: &responses}
	executor, err := registration.NewHTTPExecutor(doer)
	if err != nil {
		t.Fatal(err)
	}
	backend := qrServiceBackend{executor: executor}
	request := registration.QRCancelRequest{ID: "id", Device: registration.UUIDOnlyDevice{UUID: "uuid"}}
	if err := backend.Cancel(context.Background(), request); err == nil {
		t.Fatal("HTTP 200 error envelope accepted")
	}
	if err := backend.Cancel(context.Background(), request); err != nil {
		t.Fatalf("HTTP 200 status-zero cancel rejected: %v", err)
	}
}

type staticBodyDoer struct {
	responses *[]string
}

func (d staticBodyDoer) Do(req *http.Request) (*http.Response, error) {
	body := (*d.responses)[0]
	*d.responses = (*d.responses)[1:]
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func newQRTestLogin(t *testing.T, backend *fakeQRBackend) *qrLogin {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	connector := &KakaoConnector{Config: Config{ProfileDir: dir}}
	connector.qrBackendFactory = func(context.Context, authstate.Identity) (qrBackend, error) { return backend, nil }
	login := &qrLogin{connector: connector, backendFactory: connector.qrBackendFactory}
	return login
}

func TestQRLoginApprovalPersistsClientOwnedCredentials(t *testing.T) {
	backend := newFakeQRBackend(t, `{"status":0,"user":{"userId":42},"accessToken":"access","refreshToken":"refresh","tokenType":"bearer"}`)
	login := newQRTestLogin(t, backend)
	login.completeLogin = func(context.Context, int64) (*bridgev2.LoginStep, error) {
		return &bridgev2.LoginStep{Type: bridgev2.LoginStepTypeComplete}, nil
	}
	if _, err := login.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	original := qrPollInterval
	qrPollInterval = 0
	t.Cleanup(func() { qrPollInterval = original })
	if _, err := login.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := login.store.Snapshot()
	if err != nil || state.Credentials == nil || state.Credentials.UserID != 42 {
		t.Fatalf("persisted QR state = %#v, err=%v", state, err)
	}
	if string(state.Credentials.AutoLoginMaterial) == "" {
		t.Fatal("auto-login material was not persisted")
	}
}

func TestQRLoginCompletionErrorRetainsRecoveryCredentials(t *testing.T) {
	backend := newFakeQRBackend(t, `{"status":0,"user":{"userId":42},"accessToken":"access","refreshToken":"refresh","tokenType":"bearer"}`)
	login := newQRTestLogin(t, backend)
	login.completeLogin = func(context.Context, int64) (*bridgev2.LoginStep, error) {
		return nil, errors.New("synthetic SQLite conflict")
	}
	if _, err := login.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	original := qrPollInterval
	qrPollInterval = 0
	t.Cleanup(func() { qrPollInterval = original })
	if _, err := login.Wait(context.Background()); err == nil {
		t.Fatal("completion conflict unexpectedly succeeded")
	}
	state, err := login.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if state.Credentials == nil || state.Credentials.UserID != 42 {
		t.Fatalf("completion error discarded recovery credentials: %#v", state.Credentials)
	}
}

func TestQRLoginDeviceAuthorizationDisplayPersistsAcrossPendingPoll(t *testing.T) {
	backend := newFakeQRBackend(t, `{"status":0,"user":{"userId":42},"accessToken":"access","refreshToken":"refresh","tokenType":"bearer"}`)
	pending := func(status int64) qrPollResult {
		return qrPollResult{Result: registration.QRPollResult{Kind: registration.QRPollServerError, ServerError: &registration.ServerErrorEnvelope{Status: status}}}
	}
	backend.pollSequence = []qrPollResult{
		{Result: registration.QRPollResult{Kind: registration.QRPollServerError, ServerError: &registration.ServerErrorEnvelope{Status: -100}}, DeviceAuthCode: "A1B2", DeviceAuthRemainingSeconds: 30},
		pending(-150),
		backend.poll,
	}
	login := newQRTestLogin(t, backend)
	login.completeLogin = func(context.Context, int64) (*bridgev2.LoginStep, error) {
		return &bridgev2.LoginStep{Type: bridgev2.LoginStepTypeComplete}, nil
	}
	if _, err := login.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	original := qrPollInterval
	qrPollInterval = 0
	t.Cleanup(func() { qrPollInterval = original })
	step, err := login.Wait(context.Background())
	if err != nil || step.DisplayAndWaitParams == nil || step.DisplayAndWaitParams.Data != "A1B2" {
		t.Fatalf("device auth step = %#v, err=%v", step, err)
	}
	step, err = login.Wait(context.Background())
	if err != nil || step.Type != bridgev2.LoginStepTypeComplete {
		t.Fatalf("same-code pending poll should complete without a duplicate step: %#v, err=%v", step, err)
	}
	if backend.pollCalls != 3 {
		t.Fatalf("poll calls = %d, want initial code, pending same code, success", backend.pollCalls)
	}
}

func TestQRLoginSameDeviceAuthCodeUnregisteredPollsWithoutRepeatingStep(t *testing.T) {
	backend := newFakeQRBackend(t, `{"status":0,"user":{"userId":42},"accessToken":"access","refreshToken":"refresh","tokenType":"bearer"}`)
	unregistered := func(code string) qrPollResult {
		return qrPollResult{Result: registration.QRPollResult{Kind: registration.QRPollServerError, ServerError: &registration.ServerErrorEnvelope{Status: -100}}, DeviceAuthCode: code, DeviceAuthRemainingSeconds: 30}
	}
	backend.pollSequence = []qrPollResult{unregistered("A1B2"), unregistered("A1B2"), backend.poll}
	login := newQRTestLogin(t, backend)
	login.completeLogin = func(context.Context, int64) (*bridgev2.LoginStep, error) {
		return &bridgev2.LoginStep{Type: bridgev2.LoginStepTypeComplete}, nil
	}
	if _, err := login.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	original := qrPollInterval
	qrPollInterval = 0
	t.Cleanup(func() { qrPollInterval = original })
	step, err := login.Wait(context.Background())
	if err != nil || step.DisplayAndWaitParams == nil || step.DisplayAndWaitParams.Data != "A1B2" {
		t.Fatalf("initial device auth step = %#v, err=%v", step, err)
	}
	step, err = login.Wait(context.Background())
	if err != nil || step.Type != bridgev2.LoginStepTypeComplete {
		t.Fatalf("same-code unregistered poll should complete without a duplicate step: %#v, err=%v", step, err)
	}
	if backend.pollCalls != 3 {
		t.Fatalf("poll calls = %d, want initial code, same code, success", backend.pollCalls)
	}
}

func TestQRLoginChangedDeviceAuthCodeDisplaysNewStep(t *testing.T) {
	backend := newFakeQRBackend(t, `{"status":0,"user":{"userId":42},"accessToken":"access","refreshToken":"refresh","tokenType":"bearer"}`)
	backend.pollSequence = []qrPollResult{
		{Result: registration.QRPollResult{Kind: registration.QRPollServerError, ServerError: &registration.ServerErrorEnvelope{Status: -100}}, DeviceAuthCode: "A1B2", DeviceAuthRemainingSeconds: 30},
		{Result: registration.QRPollResult{Kind: registration.QRPollServerError, ServerError: &registration.ServerErrorEnvelope{Status: -150}}, DeviceAuthCode: "C3D4"},
		backend.poll,
	}
	login := newQRTestLogin(t, backend)
	login.completeLogin = func(context.Context, int64) (*bridgev2.LoginStep, error) {
		return &bridgev2.LoginStep{Type: bridgev2.LoginStepTypeComplete}, nil
	}
	if _, err := login.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	original := qrPollInterval
	qrPollInterval = 0
	t.Cleanup(func() { qrPollInterval = original })
	step, err := login.Wait(context.Background())
	if err != nil || step.DisplayAndWaitParams == nil || step.DisplayAndWaitParams.Data != "A1B2" {
		t.Fatalf("initial device auth step = %#v, err=%v", step, err)
	}
	step, err = login.Wait(context.Background())
	if err != nil || step.DisplayAndWaitParams == nil || step.DisplayAndWaitParams.Data != "C3D4" {
		t.Fatalf("changed device auth step = %#v, err=%v", step, err)
	}
	step, err = login.Wait(context.Background())
	if err != nil || step.Type != bridgev2.LoginStepTypeComplete {
		t.Fatalf("completion after changed code = %#v, err=%v", step, err)
	}
}

func TestQRLoginSameCodeWaitHonorsContextCancellation(t *testing.T) {
	backend := newFakeQRBackend(t, `{"status":0,"user":{"userId":42},"accessToken":"access","refreshToken":"refresh","tokenType":"bearer"}`)
	var pollNumber int
	var pollMu sync.Mutex
	backend.pollFn = func(ctx context.Context) (qrPollResult, error) {
		pollMu.Lock()
		pollNumber++
		n := pollNumber
		pollMu.Unlock()
		if n == 1 {
			return qrPollResult{Result: registration.QRPollResult{Kind: registration.QRPollServerError, ServerError: &registration.ServerErrorEnvelope{Status: -100}}, DeviceAuthCode: "A1B2", DeviceAuthRemainingSeconds: 30}, nil
		}
		<-ctx.Done()
		return qrPollResult{}, ctx.Err()
	}
	login := newQRTestLogin(t, backend)
	if _, err := login.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	original := qrPollInterval
	qrPollInterval = 0
	t.Cleanup(func() { qrPollInterval = original })
	if step, err := login.Wait(context.Background()); err != nil || step.DisplayAndWaitParams == nil {
		t.Fatalf("initial device auth step = %#v, err=%v", step, err)
	}
	backend.mu.Lock()
	backend.pollStarted = make(chan struct{})
	started := backend.pollStarted
	backend.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := login.Wait(ctx)
		done <- err
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("same-code poll did not start")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled same-code wait error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("same-code wait did not honor context cancellation")
	}
}

func TestQRLoginInvalidDeviceAuthorizationCleansProfile(t *testing.T) {
	backend := newFakeQRBackend(t, `{"status":0,"user":{"userId":42},"accessToken":"access","refreshToken":"refresh","tokenType":"bearer"}`)
	backend.pollSequence = []qrPollResult{{Result: registration.QRPollResult{Kind: registration.QRPollServerError, ServerError: &registration.ServerErrorEnvelope{Status: -100}}, DeviceAuthCode: "bad", DeviceAuthRemainingSeconds: 30}}
	login := newQRTestLogin(t, backend)
	if _, err := login.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	original := qrPollInterval
	qrPollInterval = 0
	t.Cleanup(func() { qrPollInterval = original })
	if _, err := login.Wait(context.Background()); err == nil {
		t.Fatal("invalid device authorization accepted")
	}
	if _, err := os.Stat(login.statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid device authorization profile remains: %v", err)
	}
}

func TestQRLoginCancellationCancelsRemoteChallenge(t *testing.T) {
	backend := newFakeQRBackend(t, `{"status":0,"user":{"userId":42},"accessToken":"access","refreshToken":"refresh","tokenType":"bearer"}`)
	login := newQRTestLogin(t, backend)
	if _, err := login.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	login.Cancel()
	if backend.cancels != 1 {
		t.Fatalf("remote cancellations = %d, want 1", backend.cancels)
	}
	if _, err := os.Stat(login.statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled profile still exists: %v", err)
	}
	if _, err := login.CancelStep(context.Background()); !errors.Is(err, bridgev2.ErrLoginStepCancelled) {
		t.Fatalf("CancelStep error = %v", err)
	}
}

func TestQRLoginExpiryCancelsAndFailsClosed(t *testing.T) {
	backend := newFakeQRBackend(t, `{"status":0,"user":{"userId":42},"accessToken":"access","refreshToken":"refresh","tokenType":"bearer"}`)
	login := newQRTestLogin(t, backend)
	if _, err := login.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	login.deadline = time.Now().Add(-time.Second)
	if _, err := login.Wait(context.Background()); err == nil {
		t.Fatal("expired QR was accepted")
	}
	if backend.cancels != 1 {
		t.Fatalf("expiry cancellations = %d, want 1", backend.cancels)
	}
}

func TestQRLoginConcurrentCancelAndWaitIsBounded(t *testing.T) {
	backend := newFakeQRBackend(t, `{"status":0,"user":{"userId":42},"accessToken":"access","refreshToken":"refresh","tokenType":"bearer"}`)
	login := newQRTestLogin(t, backend)
	if _, err := login.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	original := qrPollInterval
	qrPollInterval = time.Second
	t.Cleanup(func() { qrPollInterval = original })
	done := make(chan struct{})
	go func() {
		_, _ = login.Wait(context.Background())
		close(done)
	}()
	login.Cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent QR cancellation did not stop Wait")
	}
}

func TestQRLoginRejectsDuplicateStart(t *testing.T) {
	backend := newFakeQRBackend(t, `{"status":0,"user":{"userId":42},"accessToken":"access","refreshToken":"refresh","tokenType":"bearer"}`)
	login := newQRTestLogin(t, backend)
	if _, err := login.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := login.Start(context.Background()); err == nil {
		t.Fatal("duplicate Start accepted")
	}
	login.Cancel()
	if _, err := login.Start(context.Background()); err == nil {
		t.Fatal("Start after cancellation accepted")
	}
}

func TestQRLoginCancelDuringGenerateCleansProfile(t *testing.T) {
	started := make(chan struct{})
	backend := newFakeQRBackend(t, `{"status":0,"user":{"userId":42},"accessToken":"access","refreshToken":"refresh","tokenType":"bearer"}`)
	login := newQRTestLogin(t, backend)
	login.backendFactory = func(ctx context.Context, _ authstate.Identity) (qrBackend, error) {
		return blockingGenerateBackend{started: started, result: backend}, nil
	}
	startDone := make(chan struct{})
	go func() { _, _ = login.Start(context.Background()); close(startDone) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("generation did not start")
	}
	login.Cancel()
	select {
	case <-startDone:
	case <-time.After(time.Second):
		t.Fatal("cancel did not stop generation")
	}
	if login.statePath != "" {
		if _, err := os.Stat(login.statePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cancelled generation profile exists: %v", err)
		}
	}
}

type blockingGenerateBackend struct {
	started chan struct{}
	result  *fakeQRBackend
}

func (b blockingGenerateBackend) Generate(ctx context.Context, _ registration.QRGenerateRequest) (registration.QRChallenge, error) {
	close(b.started)
	<-ctx.Done()
	return registration.QRChallenge{}, ctx.Err()
}
func (b blockingGenerateBackend) Poll(ctx context.Context, req registration.QRLoginRequest) (qrPollResult, error) {
	return b.result.Poll(ctx, req)
}
func (b blockingGenerateBackend) Cancel(ctx context.Context, req registration.QRCancelRequest) error {
	return b.result.Cancel(ctx, req)
}

func TestQRLoginCancelDuringLateGenerateCancelsReturnedChallenge(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	cancelObserved := make(chan struct{})
	backend := newFakeQRBackend(t, `{"status":0,"user":{"userId":42},"accessToken":"access","refreshToken":"refresh","tokenType":"bearer"}`)
	login := newQRTestLogin(t, backend)
	login.backendFactory = func(context.Context, authstate.Identity) (qrBackend, error) {
		return lateGenerateBackend{release: release, started: started, cancelObserved: cancelObserved, result: backend}, nil
	}
	startDone := make(chan struct{})
	go func() { _, _ = login.Start(context.Background()); close(startDone) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("late generation did not start")
	}
	cancelDone := make(chan struct{})
	go func() { login.Cancel(); close(cancelDone) }()
	select {
	case <-cancelObserved:
	case <-time.After(time.Second):
		t.Fatal("generation did not observe cancellation")
	}
	close(release)
	select {
	case <-startDone:
	case <-time.After(time.Second):
		t.Fatal("late generation did not finish")
	}
	select {
	case <-cancelDone:
	case <-time.After(time.Second):
		t.Fatal("cancel did not finish")
	}
	backend.mu.Lock()
	cancels := backend.cancels
	backend.mu.Unlock()
	if cancels != 1 {
		t.Fatalf("late generated challenge cancellations = %d, want 1", cancels)
	}
}

type lateGenerateBackend struct {
	release        chan struct{}
	started        chan struct{}
	cancelObserved chan struct{}
	result         *fakeQRBackend
}

func (b lateGenerateBackend) Generate(ctx context.Context, _ registration.QRGenerateRequest) (registration.QRChallenge, error) {
	close(b.started)
	select {
	case <-ctx.Done():
		close(b.cancelObserved)
		<-b.release
	case <-b.release:
	}
	return b.result.challenge, nil
}
func (b lateGenerateBackend) Poll(ctx context.Context, req registration.QRLoginRequest) (qrPollResult, error) {
	return b.result.Poll(ctx, req)
}
func (b lateGenerateBackend) Cancel(ctx context.Context, req registration.QRCancelRequest) error {
	return b.result.Cancel(ctx, req)
}

func TestQRLoginRejectsSuccessAfterPollDeadline(t *testing.T) {
	backend := newFakeQRBackend(t, `{"status":0,"user":{"userId":42},"accessToken":"access","refreshToken":"refresh","tokenType":"bearer"}`)
	backend.pollFn = func(context.Context) (qrPollResult, error) {
		time.Sleep(20 * time.Millisecond)
		return backend.poll, nil
	}
	login := newQRTestLogin(t, backend)
	if _, err := login.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	login.deadline = time.Now().Add(10 * time.Millisecond)
	original := qrPollInterval
	qrPollInterval = 0
	t.Cleanup(func() { qrPollInterval = original })
	if _, err := login.Wait(context.Background()); err == nil {
		t.Fatal("poll success after expiry accepted")
	}
	backend.mu.Lock()
	if backend.pollCalls != 1 {
		backend.mu.Unlock()
		t.Fatalf("poll calls = %d, want 1", backend.pollCalls)
	}
	backend.mu.Unlock()
	login.Cancel()
}

func TestQRLoginCancelAfterPersistenceRetainsRecoveryProfile(t *testing.T) {
	backend := newFakeQRBackend(t, `{"status":0,"user":{"userId":42},"accessToken":"access","refreshToken":"refresh","tokenType":"bearer"}`)
	login := newQRTestLogin(t, backend)
	completeStarted := make(chan struct{})
	completeRelease := make(chan struct{})
	login.completeLogin = func(context.Context, int64) (*bridgev2.LoginStep, error) {
		close(completeStarted)
		<-completeRelease
		return &bridgev2.LoginStep{Type: bridgev2.LoginStepTypeComplete}, nil
	}
	if _, err := login.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	original := qrPollInterval
	qrPollInterval = 0
	t.Cleanup(func() { qrPollInterval = original })
	done := make(chan error, 1)
	go func() { _, err := login.Wait(context.Background()); done <- err }()
	select {
	case <-completeStarted:
	case <-time.After(time.Second):
		t.Fatal("completion did not start")
	}
	login.Cancel()
	close(completeRelease)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	hasCredentials, err := login.store.HasCredentials()
	if err != nil || !hasCredentials {
		t.Fatalf("recovery credentials lost after concurrent cancel: %v", err)
	}
}

func TestQRLoginRetriesFailedRemoteCancellation(t *testing.T) {
	backend := newFakeQRBackend(t, `{"status":0,"user":{"userId":42},"accessToken":"access","refreshToken":"refresh","tokenType":"bearer"}`)
	attempts := 0
	backend.cancelFn = func(ctx context.Context) error {
		if ctx.Err() != nil {
			t.Fatalf("cancel context was already canceled: %v", ctx.Err())
		}
		attempts++
		if attempts == 1 {
			return errors.New("synthetic cancellation failure")
		}
		return nil
	}
	login := newQRTestLogin(t, backend)
	if _, err := login.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	login.Cancel()
	login.Cancel()
	if attempts != 2 {
		t.Fatalf("cancel attempts = %d, want 2", attempts)
	}
}

func TestQRLoginConcurrentCancellationHasOneRemoteOwner(t *testing.T) {
	backend := newFakeQRBackend(t, `{"status":0,"user":{"userId":42},"accessToken":"access","refreshToken":"refresh","tokenType":"bearer"}`)
	started := make(chan struct{})
	release := make(chan struct{})
	backend.cancelFn = func(ctx context.Context) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	login := newQRTestLogin(t, backend)
	if _, err := login.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstDone, secondDone := make(chan struct{}), make(chan struct{})
	go func() { login.Cancel(); close(firstDone) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("remote cancellation did not start")
	}
	go func() { login.Cancel(); close(secondDone) }()
	close(release)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first cancellation did not finish")
	}
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("second cancellation did not finish")
	}
	backend.mu.Lock()
	attempts := backend.cancels
	backend.mu.Unlock()
	if attempts != 1 {
		t.Fatalf("remote cancellation attempts = %d, want 1", attempts)
	}
}

func TestQRLoginResumeUsesLoadUserLoginAndPrivateProfile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "resumed")
	store, err := authstate.Create(path, authstate.Config{DeviceName: "synthetic", AppVersion: "26.8.0", OSVersion: "macOS", DeviceModel: "Mac"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InstallCredentials(authstate.Credentials{UserID: 42, AccessToken: "access", AutoLoginMaterial: []byte(`{"refresh_token":"refresh","token_type":"bearer"}`)}); err != nil {
		t.Fatal(err)
	}
	kc := &KakaoConnector{Config: Config{ProfileDir: dir}}
	login := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: makeUserLoginID(42), Metadata: &UserLoginMetadata{Profile: "resumed"}}}
	if err := kc.LoadUserLogin(context.Background(), login); err != nil {
		t.Fatal(err)
	}
	if login.Client == nil {
		t.Fatal("LoadUserLogin did not restore a client")
	}
}
