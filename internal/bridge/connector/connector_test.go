package connector

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/bridgeconfig"

	"github.com/frrad/mooo/internal/authstate"
)

func TestEventDeliveryMustBeInline(t *testing.T) {
	if err := checkEventDelivery(0, false); err != nil {
		t.Fatalf("inline delivery rejected: %v", err)
	}
	if err := checkEventDelivery(64, false); !errors.Is(err, ErrUnsafeEventDelivery) {
		t.Fatalf("buffered delivery error = %v", err)
	}
	if err := checkEventDelivery(0, true); !errors.Is(err, ErrUnsafeEventDelivery) {
		t.Fatalf("async delivery error = %v", err)
	}
}

func TestStartRequiresPrivateProfileDir(t *testing.T) {
	newConnector := func(profileDir string) *KakaoConnector {
		return &KakaoConnector{
			Bridge: &bridgev2.Bridge{Config: &bridgeconfig.BridgeConfig{PortalEventBuffer: 0}},
			Config: Config{ProfileDir: profileDir},
		}
	}

	private := t.TempDir()
	if err := os.Chmod(private, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := newConnector(private).Start(context.Background()); err != nil {
		t.Fatalf("private profile dir rejected: %v", err)
	}

	shared := t.TempDir()
	if err := os.Chmod(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := newConnector(shared).Start(context.Background()); err == nil {
		t.Fatal("group/world-accessible profile dir accepted")
	}

	if err := newConnector("").Start(context.Background()); err == nil {
		t.Fatal("unconfigured profile dir accepted")
	}

	buffered := newConnector(private)
	buffered.Bridge.Config.PortalEventBuffer = 64
	if err := buffered.Start(context.Background()); !errors.Is(err, ErrUnsafeEventDelivery) {
		t.Fatalf("buffered bridge config error = %v", err)
	}
}

func TestProfileUserIDReadsCredentialsWithoutLease(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profiles")
	statePath := filepath.Join(dir, "lab-1")
	store, err := authstate.Create(statePath, authstate.Config{
		DeviceName:  "synthetic",
		AppVersion:  "1.0.0",
		OSVersion:   "15.0",
		DeviceModel: "Mac",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := profileUserID(statePath); !errors.Is(err, errProfileNotAuthorized) {
		t.Fatalf("unauthorized profile error = %v", err)
	}

	err = store.InstallCredentials(authstate.Credentials{
		UserID:            4242,
		AccessToken:       "synthetic-access-token",
		AutoLoginMaterial: []byte("synthetic-auto-login"),
	})
	if err != nil {
		t.Fatal(err)
	}
	userID, err := profileUserID(statePath)
	if err != nil || userID != 4242 {
		t.Fatalf("profileUserID = %d, %v", userID, err)
	}
	if _, err := os.Stat(statePath + ".lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reading the user ID touched the profile lease: %v", err)
	}
}
