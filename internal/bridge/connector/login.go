package connector

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/registration"
)

// UserLoginMetadata is stored with each bridge login.
type UserLoginMetadata struct {
	// Profile names the auth-state file inside the configured profile
	// directory. It is never a path.
	Profile string `json:"profile"`
}

const (
	flowImportProfile = "import-profile"
	flowQR            = "qr"
)

var errQRBackendUnavailable = errors.New("connector: QR enrollment backend is unavailable")

var qrPollInterval = 3 * time.Second

// qrBackend is the narrow bridge seam around the reviewed registration
// service. It intentionally returns typed results and never exposes raw HTTP
// bodies or logs challenge/credential values.
type qrBackend interface {
	Generate(context.Context, registration.QRGenerateRequest) (registration.QRChallenge, error)
	Poll(context.Context, registration.QRLoginRequest) (qrPollResult, error)
	Cancel(context.Context, registration.QRCancelRequest) error
}

type qrPollResult struct {
	Result                     registration.QRPollResult
	DeviceAuthCode             string
	DeviceAuthRemainingSeconds float64
}

// macQRPresentationValidator is an explicit clean-room safety policy. The
// allowlist accepts the relative account-info path established by the Android
// model and the HTTPS form used by synthetic fixtures. The structural Mac
// probe retained only path shape, so this is an explicit policy rather than a
// claim of complete Mac URL parity. The policy prevents an untrusted server
// value from becoming a displayed QR and does not claim check-key parity.
type macQRPresentationValidator struct{}

func (macQRPresentationValidator) Validate(p registration.QRPresentation) error {
	u, err := url.Parse(p.Payload)
	if err != nil {
		return errors.New("QR URL rejected")
	}
	validRelative := u.Scheme == "" && u.Host == ""
	validHTTPS := u.Scheme == "https" && u.Host == "katalk.kakao.com"
	if (!validRelative && !validHTTPS) || u.User != nil || u.Path != "/talk/account/qrCodeLogin/info.json" || u.Fragment != "" {
		return errors.New("QR URL rejected")
	}
	items := strings.Split(u.RawQuery, "&")
	if len(items) != 1 || items[0] == "" {
		return errors.New("QR URL rejected")
	}
	name, value, found := strings.Cut(items[0], "=")
	decodedName, nameErr := url.PathUnescape(name)
	decodedValue, valueErr := url.PathUnescape(value)
	if !found || nameErr != nil || valueErr != nil || decodedName != "id" || decodedValue != p.ID {
		return errors.New("QR URL rejected")
	}
	return nil
}

type macHeaderDoer struct {
	client  *http.Client
	profile registration.MacClientProfile
}

func (d macHeaderDoer) Do(req *http.Request) (*http.Response, error) {
	if err := registration.ApplyMacClientHeaders(req, d.profile); err != nil {
		return nil, err
	}
	return d.client.Do(req)
}

type qrServiceBackend struct {
	service  *registration.QRRegistrationService
	executor *registration.HTTPExecutor
}

func (b qrServiceBackend) Generate(ctx context.Context, req registration.QRGenerateRequest) (registration.QRChallenge, error) {
	return b.service.Generate(ctx, req)
}

func (b qrServiceBackend) Poll(ctx context.Context, req registration.QRLoginRequest) (qrPollResult, error) {
	result, err := b.service.Poll(ctx, req)
	if err != nil {
		return qrPollResult{}, err
	}
	return qrPollResult{Result: result, DeviceAuthCode: result.DeviceAuthCode, DeviceAuthRemainingSeconds: result.DeviceAuthRemainingSeconds}, nil
}

func (b qrServiceBackend) Cancel(ctx context.Context, req registration.QRCancelRequest) error {
	form, err := registration.BuildQRCancelRequest(req)
	if err != nil {
		return err
	}
	response, err := b.executor.ExecuteForm(ctx, form)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return errors.New("QR cancellation failed")
	}
	if len(response.Body) != 0 {
		envelope, decodeErr := registration.DecodeServerErrorEnvelope(response.Body)
		if decodeErr != nil || envelope.Status != 0 {
			return errors.New("QR cancellation failed")
		}
	}
	return nil
}

func productionQRBackend(ctx context.Context, identity authstate.Identity) (qrBackend, error) {
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	doer := macHeaderDoer{client: client, profile: registration.MacClientProfile{AppVersion: identity.Metadata.AppVersion, OSVersion: identity.Metadata.OSVersion, Language: "en"}}
	executor, err := registration.NewHTTPExecutor(doer)
	if err != nil {
		return nil, err
	}
	service, err := registration.NewQRRegistrationService(executor, macQRPresentationValidator{})
	if err != nil {
		return nil, err
	}
	return qrServiceBackend{service: service, executor: executor}, nil
}

var errAdminOnly = bridgev2.RespError{
	ErrCode:    "COM.GITHUB.FRRAD.MOOO.ADMIN_ONLY",
	Err:        "Importing an existing Kakao profile is restricted to bridge admins",
	StatusCode: 403,
}

func (kc *KakaoConnector) GetLoginFlows() []bridgev2.LoginFlow {
	return []bridgev2.LoginFlow{{
		Name:        "Import profile",
		Description: "Adopt an operator-created, already-authorized Kakao profile from the bridge's profile directory.",
		ID:          flowImportProfile,
	}, {
		Name:        "QR login",
		Description: "Authorize a new client-owned Kakao secondary-device profile with a QR code.",
		ID:          flowQR,
	}}
}

func (kc *KakaoConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if flowID != flowImportProfile {
		if flowID != flowQR {
			return nil, bridgev2.ErrInvalidLoginFlowID
		}
		factory := kc.qrBackendFactory
		if factory == nil {
			factory = productionQRBackend
		}
		return &qrLogin{connector: kc, user: user, backendFactory: factory}, nil
	}
	if !user.Permissions.Admin {
		return nil, errAdminOnly
	}
	return &importProfileLogin{connector: kc, user: user}, nil
}

type qrLogin struct {
	connector        *KakaoConnector
	user             *bridgev2.User
	backend          qrBackend
	backendFactory   func(context.Context, authstate.Identity) (qrBackend, error)
	store            *authstate.Store
	identity         authstate.Identity
	statePath        string
	profile          string
	qrID             string
	qrData           string
	deadline         time.Time
	finished         bool
	completeLogin    func(context.Context, int64) (*bridgev2.LoginStep, error)
	mu               sync.Mutex
	cancelWait       context.CancelFunc
	waitDone         chan struct{}
	waiting          bool
	started          bool
	generationDone   chan struct{}
	handoffStarted   bool
	keepProfile      bool
	deviceAuth       bool
	deviceAuthCode   string
	remoteCanceled   bool
	remoteCanceling  bool
	remoteCancelDone chan struct{}
}

var _ bridgev2.LoginProcessDisplayAndWait = (*qrLogin)(nil)
var _ bridgev2.LoginProcessStepCancel = (*qrLogin)(nil)

func (l *qrLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	if l.connector == nil || l.backendFactory == nil {
		return nil, errQRBackendUnavailable
	}
	l.mu.Lock()
	if l.started || l.finished {
		l.mu.Unlock()
		return nil, errors.New("connector: QR login is already finished or started")
	}
	l.started = true
	generationCtx, cancelGeneration := context.WithCancel(ctx)
	generationDone := make(chan struct{})
	l.generationDone, l.cancelWait = generationDone, cancelGeneration
	l.mu.Unlock()
	defer func() {
		cancelGeneration()
		l.mu.Lock()
		if l.generationDone == generationDone {
			l.generationDone, l.cancelWait = nil, nil
			close(generationDone)
		}
		l.mu.Unlock()
	}()
	profile, err := newQRProfileName()
	if err != nil {
		return nil, err
	}
	path, err := profileStatePath(l.connector.Config.ProfileDir, profile)
	if err != nil {
		return nil, err
	}
	store, err := authstate.Create(path, authstate.Config{
		DeviceName:  "mooo-bridge",
		AppVersion:  "26.8.0",
		OSVersion:   "26.6.2",
		DeviceModel: "Mac",
	})
	if err != nil {
		return nil, fmt.Errorf("connector: create QR profile: %w", err)
	}
	l.mu.Lock()
	l.store, l.statePath, l.profile = store, path, profile
	canceledBeforeGenerate := l.finished
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		keep := l.keepProfile
		l.mu.Unlock()
		if !keep {
			l.cleanupUnenrolledProfile()
		}
	}()
	if canceledBeforeGenerate {
		return nil, context.Canceled
	}
	state, err := store.Snapshot()
	if err != nil {
		return nil, err
	}
	backend, err := l.backendFactory(generationCtx, state.Identity)
	if err != nil {
		return nil, err
	}
	wireUUID, err := state.Identity.WireDeviceUUID()
	if err != nil {
		return nil, err
	}
	challenge, err := backend.Generate(generationCtx, registration.QRGenerateRequest{Device: registration.FullDevice{
		Name: state.Identity.DeviceName, UUID: wireUUID, OSVersion: state.Identity.Metadata.OSVersion, Model: state.Identity.Metadata.DeviceModel,
	}})
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.backend, l.identity, l.qrID, l.qrData = backend, state.Identity, challenge.Presentation.ID, challenge.Presentation.Payload
	finished := l.finished
	l.mu.Unlock()
	if finished {
		if !l.cancelRemote(generationCtx) {
			l.mu.Lock()
			l.keepProfile = true
			l.mu.Unlock()
		}
		return nil, context.Canceled
	}
	if strings.TrimSpace(challenge.Presentation.Payload) == "" || challenge.RemainingSeconds <= 0 {
		if !l.cancelRemote(generationCtx) {
			l.mu.Lock()
			l.keepProfile = true
			l.mu.Unlock()
		}
		return nil, errors.New("connector: invalid QR challenge lifetime")
	}
	deadline, err := qrDeadline(challenge.RemainingSeconds)
	if err != nil {
		if !l.cancelRemote(generationCtx) {
			l.mu.Lock()
			l.keepProfile = true
			l.mu.Unlock()
		}
		return nil, err
	}
	l.mu.Lock()
	l.deadline, l.keepProfile = deadline, true
	finished = l.finished
	qrData := l.qrData
	l.mu.Unlock()
	if finished {
		l.cancelRemote(context.Background())
		return nil, context.Canceled
	}
	return qrDisplayStep(qrData), nil
}

func (l *qrLogin) Cancel() {
	l.mu.Lock()
	if l.handoffStarted {
		l.mu.Unlock()
		return
	}
	l.finished = true
	cancelWait, waitDone := l.cancelWait, l.waitDone
	if waitDone == nil {
		waitDone = l.generationDone
	}
	l.mu.Unlock()
	if cancelWait != nil {
		cancelWait()
		if waitDone != nil {
			select {
			case <-waitDone:
			case <-time.After(5 * time.Second):
				return
			}
		}
	}
	l.mu.Lock()
	statePath, store := l.statePath, l.store
	l.mu.Unlock()
	if l.cancelRemote(context.Background()) {
		cleanupQRProfile(statePath, store)
	}
}

func (l *qrLogin) CancelStep(ctx context.Context) (*bridgev2.LoginStep, error) {
	l.Cancel()
	return nil, bridgev2.ErrLoginStepCancelled
}

func (l *qrLogin) Wait(ctx context.Context) (*bridgev2.LoginStep, error) {
	l.mu.Lock()
	if l.finished || l.backend == nil || l.qrID == "" {
		l.mu.Unlock()
		return nil, errors.New("connector: QR login is not active")
	}
	if l.waiting {
		l.mu.Unlock()
		return nil, errors.New("connector: QR wait already active")
	}
	backend, identity, qrID, qrData, deadline := l.backend, l.identity, l.qrID, l.qrData, l.deadline
	deviceAuth, deviceAuthCode := l.deviceAuth, l.deviceAuthCode
	l.waiting = true
	l.mu.Unlock()
	if time.Now().After(deadline) {
		l.mu.Lock()
		l.finished = true
		l.mu.Unlock()
		if l.cancelRemote(ctx) {
			l.cleanupUnenrolledProfile()
		}
		return nil, errors.New("connector: QR challenge expired")
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	l.mu.Lock()
	if l.finished || !l.waiting {
		l.mu.Unlock()
		cancel()
		return nil, errors.New("connector: QR login is not active")
	}
	l.cancelWait, l.waitDone = cancel, done
	l.mu.Unlock()
	defer func() {
		cancel()
		l.mu.Lock()
		if l.waitDone == done {
			l.cancelWait, l.waitDone, l.waiting = nil, nil, false
			close(done)
		}
		l.mu.Unlock()
	}()
	select {
	case <-runCtx.Done():
		if errors.Is(context.Cause(ctx), bridgev2.ErrLoginStepCancelled) {
			return nil, bridgev2.ErrLoginStepCancelled
		}
		if err := runCtx.Err(); err != nil {
			return nil, err
		}
		return nil, context.Canceled
	case <-time.After(qrPollInterval):
	}
	if time.Now().After(deadline) {
		l.mu.Lock()
		l.finished = true
		l.mu.Unlock()
		if l.cancelRemote(ctx) {
			l.cleanupUnenrolledProfile()
		}
		return nil, errors.New("connector: QR challenge expired")
	}
	wireUUID, err := identity.WireDeviceUUID()
	if err != nil {
		return nil, l.failQR(ctx, err)
	}
	pollCtx, cancelPoll := context.WithDeadline(runCtx, deadline)
	polled, err := backend.Poll(pollCtx, registration.QRLoginRequest{ID: qrID, Device: registration.UUIDOnlyDevice{UUID: wireUUID}})
	cancelPoll()
	if err != nil {
		if time.Now().After(deadline) {
			l.mu.Lock()
			l.finished = true
			l.mu.Unlock()
			if l.cancelRemote(ctx) {
				l.cleanupUnenrolledProfile()
			}
			return nil, errors.New("connector: QR challenge expired")
		}
		return nil, err
	}
	l.mu.Lock()
	deadline = l.deadline
	stillActive := !l.finished
	l.mu.Unlock()
	if !stillActive {
		return nil, context.Canceled
	}
	if time.Now().After(deadline) {
		l.mu.Lock()
		l.finished = true
		l.mu.Unlock()
		if l.cancelRemote(ctx) {
			l.cleanupUnenrolledProfile()
		}
		return nil, errors.New("connector: QR challenge expired")
	}
	if polled.Result.Kind == registration.QRPollSuccess {
		if polled.Result.Success == nil {
			return nil, l.failQR(ctx, errors.New("connector: QR login returned no success payload"))
		}
		credentials, userID, err := credentialsFromQR(*polled.Result.Success)
		if err != nil {
			return nil, l.failQR(ctx, err)
		}
		l.mu.Lock()
		if l.finished || l.handoffStarted {
			l.mu.Unlock()
			return nil, context.Canceled
		}
		l.handoffStarted = true
		store := l.store
		if err := store.InstallCredentials(credentials); err != nil {
			l.mu.Unlock()
			return nil, err
		}
		l.mu.Unlock()
		complete := l.completeLogin
		if complete == nil {
			complete = func(ctx context.Context, userID int64) (*bridgev2.LoginStep, error) {
				login, err := l.user.NewLogin(ctx, &database.UserLogin{ID: makeUserLoginID(userID), RemoteName: placeholderUserName(userID), Metadata: &UserLoginMetadata{Profile: l.profile}}, &bridgev2.NewLoginParams{LoadUserLogin: l.connector.LoadUserLogin})
				if err != nil {
					return nil, fmt.Errorf("connector: save QR login: %w", err)
				}
				go login.Client.Connect(login.Log.WithContext(context.Background()))
				return &bridgev2.LoginStep{Type: bridgev2.LoginStepTypeComplete, StepID: "com.github.frrad.mooo.qr.complete", Instructions: "Authorized the Kakao profile.", CompleteParams: &bridgev2.LoginCompleteParams{UserLoginID: login.ID, UserLogin: login}}, nil
			}
		}
		step, err := complete(ctx, userID)
		if err != nil {
			return nil, err
		}
		l.mu.Lock()
		l.finished = true
		l.mu.Unlock()
		return step, nil
	}
	if polled.Result.ServerError == nil {
		return nil, l.failQR(ctx, errors.New("connector: QR login returned an invalid result"))
	}
	switch polled.Result.ServerError.QROutcome() {
	case registration.OutcomePending:
		if deviceAuth {
			return &bridgev2.LoginStep{Type: bridgev2.LoginStepTypeDisplayAndWait, StepID: "com.github.frrad.mooo.qr.device-auth", Instructions: "Confirm the displayed code on the primary Kakao device.", DisplayAndWaitParams: &bridgev2.LoginDisplayAndWaitParams{Type: bridgev2.LoginDisplayTypeCode, Data: deviceAuthCode, CanCancel: true}}, nil
		}
		return qrDisplayStep(qrData), nil
	case registration.OutcomeUnregisteredDevice:
		if !validDeviceAuthCode(polled.DeviceAuthCode) {
			return nil, l.failQR(ctx, errors.New("connector: QR device authorization data unavailable"))
		}
		deadline, err := qrDeadline(polled.DeviceAuthRemainingSeconds)
		if err != nil {
			return nil, l.failQR(ctx, err)
		}
		l.mu.Lock()
		l.deadline = deadline
		l.deviceAuth, l.deviceAuthCode = true, polled.DeviceAuthCode
		l.mu.Unlock()
		return &bridgev2.LoginStep{Type: bridgev2.LoginStepTypeDisplayAndWait, StepID: "com.github.frrad.mooo.qr.device-auth", Instructions: "Confirm the displayed code on the primary Kakao device.", DisplayAndWaitParams: &bridgev2.LoginDisplayAndWaitParams{Type: bridgev2.LoginDisplayTypeCode, Data: polled.DeviceAuthCode, CanCancel: true}}, nil
	default:
		return nil, l.failQR(ctx, errors.New("connector: QR authorization failed"))
	}
}

func validDeviceAuthCode(code string) bool {
	if !utf8.ValidString(code) || utf8.RuneCountInString(code) != 4 {
		return false
	}
	return true
}

func (l *qrLogin) failQR(ctx context.Context, err error) error {
	l.mu.Lock()
	l.finished = true
	l.mu.Unlock()
	if l.cancelRemote(ctx) {
		l.cleanupUnenrolledProfile()
	}
	return err
}

func (l *qrLogin) cleanupUnenrolledProfile() {
	l.mu.Lock()
	statePath, store := l.statePath, l.store
	l.mu.Unlock()
	cleanupQRProfile(statePath, store)
}

func cleanupQRProfile(statePath string, store *authstate.Store) {
	if statePath == "" || store == nil {
		return
	}
	hasCredentials, err := store.HasCredentials()
	if err == nil && !hasCredentials {
		_ = os.Remove(statePath)
	}
}

func (l *qrLogin) cancelRemote(ctx context.Context) bool {
	l.mu.Lock()
	backend, qrID, identity := l.backend, l.qrID, l.identity
	if backend == nil || qrID == "" {
		l.mu.Unlock()
		return true
	}
	if l.remoteCanceled {
		l.mu.Unlock()
		return true
	}
	if l.remoteCanceling {
		done := l.remoteCancelDone
		l.mu.Unlock()
		select {
		case <-done:
			l.mu.Lock()
			ok := l.remoteCanceled
			l.mu.Unlock()
			return ok
		case <-time.After(5 * time.Second):
			return false
		}
	}
	l.remoteCanceling = true
	l.remoteCancelDone = make(chan struct{})
	done := l.remoteCancelDone
	l.mu.Unlock()
	wireUUID, err := identity.WireDeviceUUID()
	cancelErr := err
	if err == nil {
		cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cancelErr = backend.Cancel(cancelCtx, registration.QRCancelRequest{ID: qrID, Device: registration.UUIDOnlyDevice{UUID: wireUUID}})
		cancel()
	}
	l.mu.Lock()
	if cancelErr == nil {
		l.remoteCanceled = true
	}
	l.remoteCanceling = false
	close(done)
	ok := cancelErr == nil
	l.mu.Unlock()
	return ok
}

func qrDisplayStep(data string) *bridgev2.LoginStep {
	return &bridgev2.LoginStep{Type: bridgev2.LoginStepTypeDisplayAndWait, StepID: "com.github.frrad.mooo.qr", Instructions: "Scan this QR code with the KakaoTalk primary device.", DisplayAndWaitParams: &bridgev2.LoginDisplayAndWaitParams{Type: bridgev2.LoginDisplayTypeQR, Data: data, CanCancel: true}}
}

func newQRProfileName() (string, error) {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return "qr-" + hex.EncodeToString(bytes[:]), nil
}

func qrDeadline(seconds float64) (time.Time, error) {
	if seconds <= 0 || seconds != seconds || seconds > float64((1<<63-1)/int64(time.Second)) {
		return time.Time{}, errors.New("connector: invalid QR challenge lifetime")
	}
	return time.Now().Add(time.Duration(seconds * float64(time.Second))), nil
}

func credentialsFromQR(result registration.QRLoginSuccess) (authstate.Credentials, int64, error) {
	userID, ok := result.UserID.Value()
	accessToken, accessOK := result.AccessToken.Value()
	refreshToken, refreshOK := result.RefreshToken.Value()
	tokenType, tokenTypeOK := result.TokenType.Value()
	if !ok || userID <= 0 || !accessOK || strings.TrimSpace(accessToken) == "" || !refreshOK || strings.TrimSpace(refreshToken) == "" || !tokenTypeOK || strings.TrimSpace(tokenType) == "" {
		return authstate.Credentials{}, 0, errors.New("connector: QR success omitted required credentials")
	}
	material := map[string]string{"refresh_token": refreshToken, "token_type": tokenType}
	if value, present := result.AutoLoginAccountID.Value(); present {
		material["auto_login_account_id"] = value
	}
	if value, present := result.DisplayAccountID.Value(); present {
		material["display_account_id"] = value
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		return authstate.Credentials{}, 0, errors.New("connector: encode QR credentials")
	}
	return authstate.Credentials{UserID: userID, AccessToken: accessToken, AutoLoginMaterial: encoded}, userID, nil
}

// importProfileLogin adopts an existing profile by name. Only bridge admins
// can start it, and the name is confined to the configured directory, so a
// login request cannot reach arbitrary files on the host.
type importProfileLogin struct {
	connector *KakaoConnector
	user      *bridgev2.User
}

var _ bridgev2.LoginProcessUserInput = (*importProfileLogin)(nil)

const profileField = "profile"

func (l *importProfileLogin) Start(ctx context.Context) (*bridgev2.LoginStep, error) {
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       "com.github.frrad.mooo.import.profile",
		Instructions: "Enter the name of an authorized profile in the bridge's profile directory.",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{{
				Type:    bridgev2.LoginInputFieldTypeUsername,
				ID:      profileField,
				Name:    "Profile name",
				Pattern: profileNamePattern.String(),
			}},
		},
	}, nil
}

func (l *importProfileLogin) Cancel() {}

func (l *importProfileLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	name := input[profileField]
	statePath, err := profileStatePath(l.connector.Config.ProfileDir, name)
	if err != nil {
		return nil, err
	}
	userID, err := profileUserID(statePath)
	if err != nil {
		return nil, err
	}
	login, err := l.user.NewLogin(ctx, &database.UserLogin{
		ID:         makeUserLoginID(userID),
		RemoteName: placeholderUserName(userID),
		Metadata:   &UserLoginMetadata{Profile: name},
	}, &bridgev2.NewLoginParams{
		LoadUserLogin: l.connector.LoadUserLogin,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to save login: %w", err)
	}
	go login.Client.Connect(login.Log.WithContext(context.Background()))
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeComplete,
		StepID:       "com.github.frrad.mooo.import.complete",
		Instructions: "Imported the Kakao profile.",
		CompleteParams: &bridgev2.LoginCompleteParams{
			UserLoginID: login.ID,
			UserLogin:   login,
		},
	}, nil
}

var errProfileNotAuthorized = errors.New("connector: profile has no credentials; complete device authorization first")

// profileUserID reads the Kakao user ID from a profile without taking its
// lease or touching the network.
func profileUserID(statePath string) (int64, error) {
	store, err := authstate.Open(statePath)
	if err != nil {
		return 0, err
	}
	state, err := store.Snapshot()
	if err != nil {
		return 0, err
	}
	if state.Credentials == nil || state.Credentials.UserID <= 0 {
		return 0, errProfileNotAuthorized
	}
	return state.Credentials.UserID, nil
}
