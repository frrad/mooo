package connector

import (
	"context"
	"errors"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/bridgeconfig"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
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
	defer raw.RawDB.Close()
	network := &KakaoConnector{}
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
	secondMeta := &UserLoginMetadata{Profile: "replacement-profile"}
	if _, err := user.NewLogin(ctx, &database.UserLogin{ID: loginID, RemoteName: "replacement", Metadata: secondMeta}, &bridgev2.NewLoginParams{
		LoadUserLogin: func(context.Context, *bridgev2.UserLogin) error {
			loads++
			return errors.New("replacement owner must not load")
		},
		DontReuseExisting: true,
	}); err == nil {
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
