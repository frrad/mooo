package connector

import (
	"context"
	"errors"
	"fmt"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/frrad/mooo/internal/authstate"
)

// UserLoginMetadata is stored with each bridge login.
type UserLoginMetadata struct {
	// Profile names the auth-state file inside the configured profile
	// directory. It is never a path.
	Profile string `json:"profile"`
}

const flowImportProfile = "import-profile"

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
	}}
}

func (kc *KakaoConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if flowID != flowImportProfile {
		return nil, bridgev2.ErrInvalidLoginFlowID
	}
	if !user.Permissions.Admin {
		return nil, errAdminOnly
	}
	return &importProfileLogin{connector: kc, user: user}, nil
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
