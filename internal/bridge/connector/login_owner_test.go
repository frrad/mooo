package connector

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/bridgeconfig"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/authstate"
)

// embeddedMatrix supplies the framework interface without a live Matrix
// service; NewBridge still exercises its real SQLite/cache ownership paths.
type embeddedMatrix struct{ bridgev2.MatrixConnector }

func (m *embeddedMatrix) Init(*bridgev2.Bridge)         {}
func (m *embeddedMatrix) BotIntent() bridgev2.MatrixAPI { return nil }

func TestNewLoginRejectsReuseWithoutReplacingActiveOwner(t *testing.T) {
	ctx := context.Background()
	raw, err := dbutil.NewWithDialect(":memory:", "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.RawDB.Close() }()
	profileDir := t.TempDir()
	if err := os.Chmod(profileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(profileDir, "existing-profile")
	store, err := authstate.Create(profilePath, authstate.Config{DeviceName: "synthetic", AppVersion: "1", OSVersion: "1", DeviceModel: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InstallCredentials(authstate.Credentials{UserID: 4242, AccessToken: "access", AutoLoginMaterial: []byte("auto")}); err != nil {
		t.Fatal(err)
	}
	network := &KakaoConnector{Config: Config{ProfileDir: profileDir}}
	br := bridgev2.NewBridge(networkid.BridgeID("test"), raw, zerolog.Nop(), &bridgeconfig.BridgeConfig{}, &embeddedMatrix{}, network, func(*bridgev2.Bridge) bridgev2.CommandProcessor { return nil })
	br.BackgroundCtx = context.Background()
	if err := br.DB.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := br.GetUserByMXID(ctx, id.UserID("@fred:example.org"))
	if err != nil {
		t.Fatal(err)
	}
	var active *KakaoClient
	loads := 0
	loginID := makeUserLoginID(4242)
	profile := &UserLoginMetadata{Profile: "existing-profile"}
	first, err := user.NewLogin(ctx, &database.UserLogin{ID: loginID, RemoteName: "existing", Metadata: profile}, &bridgev2.NewLoginParams{
		LoadUserLogin: func(_ context.Context, login *bridgev2.UserLogin) error {
			loads++
			active = newKakaoClient(login, 4242, func() (kakaoClient, error) { return &fakeKakao{}, nil })
			login.Client = active
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	importer := &importProfileLogin{connector: network, user: user}
	if _, err := importer.SubmitUserInput(ctx, map[string]string{profileField: "existing-profile"}); err == nil {
		t.Fatal("duplicate login was accepted")
	}
	if loads != 1 {
		t.Fatalf("LoadUserLogin calls = %d, want one", loads)
	}
	if first.Client != active {
		t.Fatal("active client owner was replaced")
	}
	if first.RemoteName != "existing" {
		t.Fatalf("existing remote name = %q, want existing", first.RemoteName)
	}
	gotMeta, ok := first.Metadata.(*UserLoginMetadata)
	if !ok || gotMeta.Profile != "existing-profile" {
		t.Fatalf("existing profile metadata = %#v, want existing-profile", first.Metadata)
	}
	if br.GetCachedUserLoginByID(loginID) != first {
		t.Fatal("bridge cache no longer points at the active owner")
	}
}

func TestImportFlowRejectsExistingOwnerWithoutReload(t *testing.T) {
	ctx := context.Background()
	profileDir := t.TempDir()
	if err := os.Chmod(profileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(profileDir, "existing-profile")
	store, err := authstate.Create(profilePath, authstate.Config{DeviceName: "synthetic", AppVersion: "1", OSVersion: "1", DeviceModel: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InstallCredentials(authstate.Credentials{UserID: 4242, AccessToken: "access", AutoLoginMaterial: []byte("auto")}); err != nil {
		t.Fatal(err)
	}
	raw, err := dbutil.NewWithDialect(":memory:", "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.RawDB.Close() }()
	network := &KakaoConnector{Config: Config{ProfileDir: profileDir}}
	br := bridgev2.NewBridge(networkid.BridgeID("test"), raw, zerolog.Nop(), &bridgeconfig.BridgeConfig{}, &embeddedMatrix{}, network, func(*bridgev2.Bridge) bridgev2.CommandProcessor { return nil })
	br.BackgroundCtx = context.Background()
	if err := br.DB.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := br.GetUserByMXID(ctx, id.UserID("@fred:example.org"))
	if err != nil {
		t.Fatal(err)
	}
	active := newKakaoClient(&bridgev2.UserLogin{}, 4242, func() (kakaoClient, error) { return &fakeKakao{}, nil })
	loads := 0
	loginID := makeUserLoginID(4242)
	first, err := user.NewLogin(ctx, &database.UserLogin{ID: loginID, RemoteName: "existing", Metadata: &UserLoginMetadata{Profile: "existing-profile"}}, &bridgev2.NewLoginParams{
		LoadUserLogin: func(_ context.Context, login *bridgev2.UserLogin) error {
			loads++
			login.Client = active
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	importer := &importProfileLogin{connector: network, user: user}
	if _, err := importer.SubmitUserInput(ctx, map[string]string{profileField: "existing-profile"}); err == nil {
		t.Fatal("import flow accepted an existing login owner")
	}
	if loads != 1 || first.Client != active {
		t.Fatalf("import replaced active owner: loads=%d clientChanged=%v", loads, first.Client != active)
	}
}

func TestQRFlowRejectsExistingOwnerAndRetainsCredentials(t *testing.T) {
	ctx := context.Background()
	raw, err := dbutil.NewWithDialect(":memory:", "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.RawDB.Close() }()
	backend := newFakeQRBackend(t, `{"status":0,"user":{"userId":42},"accessToken":"access","refreshToken":"refresh","tokenType":"bearer"}`)
	network := &KakaoConnector{Config: Config{ProfileDir: t.TempDir()}}
	if err := os.Chmod(network.Config.ProfileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	network.qrBackendFactory = func(context.Context, authstate.Identity) (qrBackend, error) { return backend, nil }
	br := bridgev2.NewBridge(networkid.BridgeID("test"), raw, zerolog.Nop(), &bridgeconfig.BridgeConfig{}, &embeddedMatrix{}, network, func(*bridgev2.Bridge) bridgev2.CommandProcessor { return nil })
	br.BackgroundCtx = context.Background()
	if err := br.DB.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := br.GetUserByMXID(ctx, id.UserID("@fred:example.org"))
	if err != nil {
		t.Fatal(err)
	}
	active := newKakaoClient(&bridgev2.UserLogin{}, 42, func() (kakaoClient, error) { return &fakeKakao{}, nil })
	first, err := user.NewLogin(ctx, &database.UserLogin{ID: makeUserLoginID(42), RemoteName: "existing", Metadata: &UserLoginMetadata{Profile: "old-profile"}}, &bridgev2.NewLoginParams{
		LoadUserLogin: func(_ context.Context, login *bridgev2.UserLogin) error { login.Client = active; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	login := &qrLogin{connector: network, user: user, backendFactory: network.qrBackendFactory}
	oldInterval := qrPollInterval
	qrPollInterval = 0
	t.Cleanup(func() { qrPollInterval = oldInterval })
	if _, err := login.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := login.Wait(ctx); err == nil {
		t.Fatal("QR flow accepted an existing login owner")
	}
	state, err := login.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if state.Credentials == nil || state.Credentials.UserID != 42 {
		t.Fatal("QR conflict discarded recovery credentials")
	}
	if first.Client != active {
		t.Fatal("QR conflict replaced active owner")
	}
}
