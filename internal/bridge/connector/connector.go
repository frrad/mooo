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
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/shadow"
)

//go:embed example-config.yaml
var exampleConfig string

// Config is the network section of the bridge configuration.
type Config struct {
	// ProfileDir holds operator-created Kakao profiles. Each profile is a
	// private auth-state file plus its lock and continuity siblings.
	ProfileDir string `yaml:"profile_dir"`

	// BSONShadow optionally enables the research comparison of every incoming
	// LOCO body against the source-compatible observed decoder. Off by default.
	BSONShadow BSONShadowConfig `yaml:"bson_shadow"`

	// NetworkIcon optionally names an operator-uploaded mxc:// image that
	// Matrix clients show as the KakaoTalk network icon. Empty publishes none.
	NetworkIcon string `yaml:"network_icon"`
}

// BSONShadowConfig is the bridge-facing form of shadow.Config.
type BSONShadowConfig struct {
	// Mode is "off", "log" or "panic"; empty means off. Panic lets any remote
	// sender who can cause a discrepancy crash the bridge.
	Mode string `yaml:"mode"`
	// DumpDir optionally receives private raw-body dumps (mode 0700 required).
	DumpDir string `yaml:"dump_dir"`
}

func upgradeConfig(helper up.Helper) {
	helper.Copy(up.Str, "profile_dir")
	helper.Copy(up.Str, "bson_shadow", "mode")
	helper.Copy(up.Str, "bson_shadow", "dump_dir")
	helper.Copy(up.Str, "network_icon")
}

// ErrUnsafeEventDelivery reports a bridge configuration under which
// QueueRemoteEvent returns before an event has been handled, which would
// let the connector commit a Kakao message that never reached Matrix.
var ErrUnsafeEventDelivery = errors.New("connector: bridge.portal_event_buffer must be 0 and bridge.async_events must be false")

// KakaoConnector is the bridgev2 NetworkConnector for KakaoTalk.
type KakaoConnector struct {
	Bridge *bridgev2.Bridge
	Config Config

	// qrBackendFactory is deliberately injected rather than guessed from the
	// official client. The reviewed Mac QR check-key validator and complete
	// success-material mapping are not yet established in public evidence.
	// Production therefore fails closed until a reviewed backend is supplied;
	// tests use a fake transport here.
	qrBackendFactory func(context.Context, authstate.Identity) (qrBackend, error)

	// packetObserver is the configured BSON shadow observer, built by Start.
	// Nil, the default, adds no per-packet work.
	packetObserver func(loco.Packet)
}

var (
	_ bridgev2.NetworkConnector               = (*KakaoConnector)(nil)
	_ bridgev2.PortalBridgeInfoFillingNetwork = (*KakaoConnector)(nil)
)

func (kc *KakaoConnector) Init(bridge *bridgev2.Bridge) {
	kc.Bridge = bridge
	installHistoryMatrix(bridge)
	if processor, ok := bridge.Commands.(*commands.Processor); ok {
		processor.AddHandlers(commandReconcileGroup, commandCompleteGroupInvitations, commandGroupHistory, commandReconnect)
	}
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
	if icon := kc.Config.NetworkIcon; icon != "" {
		if uri, err := id.ParseContentURI(icon); err != nil || !uri.IsValid() {
			return errors.New("connector: network.network_icon must be an mxc:// content URI")
		}
	}
	return kc.configureBSONShadow()
}

// configureBSONShadow validates the shadow settings and, when a mode is set,
// builds the packet observer every profile client is opened with. It reports
// discrepancies through the bridge logger. Events are log-safe; raw bodies
// only ever go to the private dump directory.
func (kc *KakaoConnector) configureBSONShadow() error {
	mode, err := shadow.ParseMode(kc.Config.BSONShadow.Mode)
	if err != nil {
		return fmt.Errorf("connector: network.bson_shadow.mode: %w", err)
	}
	if dir := kc.Config.BSONShadow.DumpDir; dir != "" {
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("connector: network.bson_shadow.dump_dir: %w", err)
		}
		if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
			return errors.New("connector: network.bson_shadow.dump_dir must be a directory accessible only by its owner")
		}
	}
	log := kc.Bridge.Log.With().Str("component", "bson_shadow").Logger()
	kc.packetObserver = shadow.NewObserver(shadow.Config{
		Mode:    mode,
		DumpDir: kc.Config.BSONShadow.DumpDir,
		Report: func(event shadow.Event) {
			entry := log.Warn().
				Str("method", event.Method).
				Uint32("packet_id", event.PacketID).
				Int("body_len", event.BodyLen).
				Str("body_sha256", event.BodySHA256).
				Interface("discrepancies", event.Discrepancies)
			if event.DumpPath != "" {
				entry = entry.Str("dump_path", event.DumpPath)
			}
			if event.DumpErr != nil {
				entry = entry.AnErr("dump_error", event.DumpErr)
			}
			entry.Msg("BSON shadow decoder disagrees with production decoder")
		},
	})
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
		NetworkIcon:          id.ContentURIString(kc.Config.NetworkIcon),
		NetworkID:            "kakaotalk",
		BeeperBridgeType:     "kakaotalk",
		DefaultPort:          29340,
		DefaultCommandPrefix: "!kakao",
	}
}

// FillPortalBridgeInfo adds an m.bridge network section carrying the
// configured icon. Beeper Desktop ignores the protocol avatar but shows the
// network avatar as the account's sidebar icon. Every portal shares one
// network ID, so the account stays a single sidebar entry. Without an icon
// the section is omitted and the bridge info is unchanged.
func (kc *KakaoConnector) FillPortalBridgeInfo(_ *bridgev2.Portal, content *event.BridgeEventContent) {
	if kc.Config.NetworkIcon == "" {
		return
	}
	name := kc.GetName()
	content.Network = &event.BridgeInfoSection{
		ID:          name.NetworkID,
		DisplayName: name.DisplayName,
		AvatarURL:   name.NetworkIcon,
	}
}

func (kc *KakaoConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		Portal:    func() any { return &KakaoPortalMetadata{} },
		Message:   func() any { return &KakaoMessageMetadata{} },
		UserLogin: func() any { return &UserLoginMetadata{} },
	}
}

func (kc *KakaoConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{Provisioning: bridgev2.ProvisioningCapabilities{
		GroupCreation: map[string]bridgev2.GroupTypeCapabilities{"regular": {
			TypeDescription: "Regular KakaoTalk group in the selected Matrix room",
			Name:            bridgev2.GroupFieldCapability{Allowed: true},
			Participants:    bridgev2.GroupFieldCapability{Allowed: true, MinLength: 2, MaxLength: 1000},
		}},
	}}
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
	login.Client = newKakaoClient(login, newBridgeStore(login.Bridge.DB), userID, func() (kakaoClient, error) {
		return openProfileClient(statePath, kc.packetObserver)
	})
	return nil
}
