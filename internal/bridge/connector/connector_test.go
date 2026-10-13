package connector

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/bridgeconfig"
	"maunium.net/go/mautrix/event"

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

func TestStartValidatesBSONShadowConfig(t *testing.T) {
	profiles := t.TempDir()
	if err := os.Chmod(profiles, 0o700); err != nil {
		t.Fatal(err)
	}
	newConnector := func(shadow BSONShadowConfig) *KakaoConnector {
		return &KakaoConnector{
			Bridge: &bridgev2.Bridge{Config: &bridgeconfig.BridgeConfig{PortalEventBuffer: 0}},
			Config: Config{ProfileDir: profiles, BSONShadow: shadow},
		}
	}

	if err := newConnector(BSONShadowConfig{Mode: "loud"}).Start(context.Background()); err == nil {
		t.Fatal("unknown shadow mode accepted")
	}

	shared := t.TempDir()
	if err := os.Chmod(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := newConnector(BSONShadowConfig{Mode: "log", DumpDir: shared}).Start(context.Background()); err == nil {
		t.Fatal("group/world-accessible dump dir accepted")
	}

	if err := newConnector(BSONShadowConfig{Mode: "log", DumpDir: filepath.Join(profiles, "missing")}).Start(context.Background()); err == nil {
		t.Fatal("missing dump dir accepted")
	}

	private := t.TempDir()
	if err := os.Chmod(private, 0o700); err != nil {
		t.Fatal(err)
	}
	logging := newConnector(BSONShadowConfig{Mode: "log", DumpDir: private})
	if err := logging.Start(context.Background()); err != nil {
		t.Fatalf("valid shadow config rejected: %v", err)
	}
	if logging.packetObserver == nil {
		t.Fatal("log mode built no packet observer")
	}
}

func TestStartLeavesBSONShadowOffByDefault(t *testing.T) {
	profiles := t.TempDir()
	if err := os.Chmod(profiles, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"", "off"} {
		kc := &KakaoConnector{
			Bridge: &bridgev2.Bridge{Config: &bridgeconfig.BridgeConfig{PortalEventBuffer: 0}},
			Config: Config{ProfileDir: profiles, BSONShadow: BSONShadowConfig{Mode: mode}},
		}
		if err := kc.Start(context.Background()); err != nil {
			t.Fatalf("mode %q rejected: %v", mode, err)
		}
		if kc.packetObserver != nil {
			t.Fatalf("mode %q built a packet observer; the bridge must not decode every packet twice by default", mode)
		}
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

func TestGetNamePublishesConfiguredNetworkIcon(t *testing.T) {
	unset := (&KakaoConnector{}).GetName()
	if unset.NetworkIcon != "" {
		t.Fatalf("unconfigured network icon = %q, want empty", unset.NetworkIcon)
	}

	kc := &KakaoConnector{Config: Config{NetworkIcon: "mxc://example.org/syntheticKakaoIcon"}}
	name := kc.GetName()
	if name.NetworkIcon != "mxc://example.org/syntheticKakaoIcon" {
		t.Fatalf("network icon = %q", name.NetworkIcon)
	}
	section := name.AsBridgeInfoSection()
	if section.AvatarURL != "mxc://example.org/syntheticKakaoIcon" {
		t.Fatalf("m.bridge protocol avatar_url = %q", section.AvatarURL)
	}
	if section.ID != "kakaotalk" {
		t.Fatalf("m.bridge protocol id = %q", section.ID)
	}
}

func TestStartValidatesNetworkIcon(t *testing.T) {
	profiles := t.TempDir()
	if err := os.Chmod(profiles, 0o700); err != nil {
		t.Fatal(err)
	}
	newConnector := func(icon string) *KakaoConnector {
		return &KakaoConnector{
			Bridge: &bridgev2.Bridge{Config: &bridgeconfig.BridgeConfig{PortalEventBuffer: 0}},
			Config: Config{ProfileDir: profiles, NetworkIcon: icon},
		}
	}

	for _, icon := range []string{"", "mxc://example.org/syntheticKakaoIcon"} {
		if err := newConnector(icon).Start(context.Background()); err != nil {
			t.Fatalf("network icon %q rejected: %v", icon, err)
		}
	}
	for _, icon := range []string{
		"https://example.org/kakao.png",
		"mxc://example.org",
		"mxc:///syntheticKakaoIcon",
		"kakao.png",
	} {
		if err := newConnector(icon).Start(context.Background()); err == nil {
			t.Fatalf("network icon %q accepted; it must be an mxc:// content URI", icon)
		}
	}
}

func TestFillPortalBridgeInfoPublishesNetworkIcon(t *testing.T) {
	portal := &bridgev2.Portal{}

	unset := event.BridgeEventContent{}
	(&KakaoConnector{}).FillPortalBridgeInfo(portal, &unset)
	if unset.Network != nil {
		t.Fatalf("network section without a configured icon = %+v, want none", unset.Network)
	}

	kc := &KakaoConnector{Config: Config{NetworkIcon: "mxc://example.org/syntheticKakaoIcon"}}
	content := event.BridgeEventContent{}
	kc.FillPortalBridgeInfo(portal, &content)
	if content.Network == nil {
		t.Fatal("configured icon published no network section")
	}
	if content.Network.ID != "kakaotalk" {
		t.Fatalf("network id = %q", content.Network.ID)
	}
	if content.Network.DisplayName != "KakaoTalk" {
		t.Fatalf("network displayname = %q", content.Network.DisplayName)
	}
	if content.Network.AvatarURL != "mxc://example.org/syntheticKakaoIcon" {
		t.Fatalf("network avatar_url = %q", content.Network.AvatarURL)
	}
}
