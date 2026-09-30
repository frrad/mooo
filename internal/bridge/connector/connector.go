// Package connector implements the mautrix bridgev2 network connector for
// KakaoTalk. It adapts the protocol client in internal/client to the bridge
// framework without the client depending on Matrix.
package connector

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"

	up "go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

//go:embed example-config.yaml
var exampleConfig string

// Config is the network section of the bridge configuration.
type Config struct {
	// ProfileDir holds operator-created Kakao profiles. Each profile is a
	// private auth-state file plus its lock and continuity siblings.
	ProfileDir string `yaml:"profile_dir"`
}

func upgradeConfig(helper up.Helper) {
	helper.Copy(up.Str, "profile_dir")
}

// ErrUnsafeEventDelivery reports a bridge configuration under which
// QueueRemoteEvent returns before an event has been handled, which would
// let the connector commit a Kakao message that never reached Matrix.
var ErrUnsafeEventDelivery = errors.New("connector: bridge.portal_event_buffer must be 0 and bridge.async_events must be false")

// KakaoConnector is the bridgev2 NetworkConnector for KakaoTalk.
type KakaoConnector struct {
	Bridge *bridgev2.Bridge
	Config Config
}

var (
	_ bridgev2.NetworkConnector = (*KakaoConnector)(nil)
)

func (kc *KakaoConnector) Init(bridge *bridgev2.Bridge) {
	kc.Bridge = bridge
}

func (kc *KakaoConnector) Start(ctx context.Context) error {
	if err := checkEventDelivery(kc.Bridge.Config.PortalEventBuffer, kc.Bridge.Config.AsyncEvents); err != nil {
		return err
	}
	if kc.Config.ProfileDir == "" {
		return errors.New("connector: network.profile_dir is not configured")
	}
	info, err := os.Stat(kc.Config.ProfileDir)
	if err != nil {
		return fmt.Errorf("connector: profile directory: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("connector: profile_dir must be a directory accessible only by its owner")
	}
	return nil
}

// checkEventDelivery fails closed unless remote events are handled inline,
// which is what makes EventHandlingResult a reliable commit signal.
func checkEventDelivery(portalEventBuffer int, asyncEvents bool) error {
	if portalEventBuffer != 0 || asyncEvents {
		return ErrUnsafeEventDelivery
	}
	return nil
}

func (kc *KakaoConnector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:          "KakaoTalk",
		NetworkURL:           "https://www.kakaocorp.com/page/service/service/KakaoTalk",
		NetworkID:            "kakaotalk",
		BeeperBridgeType:     "kakaotalk",
		DefaultPort:          29340,
		DefaultCommandPrefix: "!kakao",
	}
}

func (kc *KakaoConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		UserLogin: func() any { return &UserLoginMetadata{} },
	}
}

func (kc *KakaoConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{}
}

func (kc *KakaoConnector) GetConfig() (string, any, up.Upgrader) {
	return exampleConfig, &kc.Config, up.SimpleUpgrader(upgradeConfig)
}

func (kc *KakaoConnector) GetBridgeInfoVersion() (info, capabilities int) {
	return 1, 1
}

func (kc *KakaoConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	meta, ok := login.Metadata.(*UserLoginMetadata)
	if !ok {
		return errors.New("connector: user login has no Kakao metadata")
	}
	statePath, err := profileStatePath(kc.Config.ProfileDir, meta.Profile)
	if err != nil {
		return err
	}
	userID, err := parseUserID(networkIDString(login.ID))
	if err != nil {
		return err
	}
	login.Client = newKakaoClient(login, userID, func() (kakaoClient, error) {
		return openProfileClient(statePath)
	})
	return nil
}
