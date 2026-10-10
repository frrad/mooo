// Package authstate stores the identity and authentication result owned by the
// clean-room secondary-device client.
//
// It deliberately accepts an explicit state-file path. It does not discover,
// inspect, or import any official KakaoTalk application container.
package authstate

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1" // The reviewed Mac device-identifier profile requires SHA-1.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/frrad/mooo/internal/privatejson"
)

const (
	// StateVersion is the on-disk schema version. Unknown versions are never
	// migrated implicitly: callers must deliberately handle that migration.
	StateVersion uint32 = 1
	// MacMetadataVersion versions the synthetic Mac-compatible identity
	// metadata independently of the surrounding state file.
	MacMetadataVersion uint32 = 1
)

var (
	ErrInvalidPath           = errors.New("authstate: invalid state path")
	ErrInvalidConfig         = errors.New("authstate: invalid identity configuration")
	ErrAlreadyExists         = errors.New("authstate: state already exists")
	ErrNotFound              = errors.New("authstate: state not found")
	ErrCorrupt               = errors.New("authstate: corrupt state")
	ErrVersionMismatch       = errors.New("authstate: unsupported state version")
	ErrUnsafePermissions     = errors.New("authstate: unsafe permissions")
	ErrIncompleteCredentials = errors.New("authstate: incomplete credentials")
	ErrInvalidCredentials    = errors.New("authstate: invalid credentials")
	ErrCredentialsChanged    = errors.New("authstate: credentials changed")
)

// Config describes the identity to create. All values are client-supplied;
// none are read from an installed KakaoTalk client.
type Config struct {
	DeviceName  string
	AppVersion  string
	OSVersion   string
	DeviceModel string
}

// MacMetadata is the versioned, implementation-neutral metadata carried with
// the generated device identity. It intentionally contains no account or
// authentication material.
type MacMetadata struct {
	Version     uint32 `json:"version"`
	Platform    string `json:"platform"`
	AppVersion  string `json:"app_version"`
	OSVersion   string `json:"os_version"`
	DeviceModel string `json:"device_model"`
}

// Identity is generated once for a logical secondary device and reused on
// subsequent loads.
type Identity struct {
	DeviceUUID string      `json:"device_uuid"`
	DeviceName string      `json:"device_name"`
	Metadata   MacMetadata `json:"metadata"`
}

// WireDeviceUUID derives the identifier sent by the reviewed Mac profile from
// the client-owned UUID seed. The seed remains the durable local identity;
// callers must use this derived value consistently for registration and LOCO.
func (i Identity) WireDeviceUUID() (string, error) {
	if !validUUID(i.DeviceUUID) {
		return "", ErrCorrupt
	}
	input := []byte(i.DeviceUUID)
	sha1Sum := sha1.Sum(input)
	sha256Sum := sha256.Sum256(input)
	combined := make([]byte, 0, len(sha1Sum)+len(sha256Sum))
	combined = append(combined, sha1Sum[:]...)
	combined = append(combined, sha256Sum[:]...)
	return base64.StdEncoding.EncodeToString(combined), nil
}

// String never exposes the device UUID, name, or metadata. Identity values
// are account/device-sensitive and must remain redacted in diagnostics.
func (i Identity) String() string { return "[redacted identity]" }

// GoString keeps %#v formatting redacted as well.
func (i Identity) GoString() string { return "[redacted identity]" }

// Credentials are installed only after the server has returned the complete
// authentication result. AutoLoginMaterial is opaque to this package.
type Credentials struct {
	UserID            int64  `json:"user_id"`
	AccessToken       string `json:"access_token"`
	AutoLoginMaterial []byte `json:"auto_login_material"`
}

// Clone returns an independent copy, including the opaque material.
func (c Credentials) Clone() Credentials {
	c.AutoLoginMaterial = append([]byte(nil), c.AutoLoginMaterial...)
	return c
}

// String never includes credential values.
func (c Credentials) String() string { return "[redacted credentials]" }

// GoString keeps %#v formatting redacted as well.
func (c Credentials) GoString() string { return "[redacted credentials]" }

// State is a complete persisted snapshot. A nil Credentials means that this
// client has not completed authorization yet.
type State struct {
	Version     uint32       `json:"version"`
	Identity    Identity     `json:"identity"`
	Credentials *Credentials `json:"credentials,omitempty"`
}

// String never includes authentication material or its values.
func (s State) String() string {
	return fmt.Sprintf("authstate{version:%d identity:[redacted] credentials:%t}",
		s.Version, s.Credentials != nil)
}

// GoString keeps %#v formatting redacted as well.
func (s State) GoString() string { return s.String() }

// Store is a handle to one explicit state file. Store methods serialize local
// callers; atomic replacement also prevents readers from observing a partial
// write.
type Store struct {
	path string
	mu   sync.Mutex
}

// Create generates and persists a fresh client-owned identity. It refuses to
// overwrite an existing state file, ensuring the UUID is generated once.
func Create(path string, cfg Config) (*Store, error) {
	path, err := validatePath(path)
	if err != nil {
		return nil, err
	}
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	if err := ensurePrivateDir(dir); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrUnsafePermissions
		}
		return nil, ErrAlreadyExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, ErrCorrupt
	}

	uuid, err := newUUID()
	if err != nil {
		return nil, ErrCorrupt
	}
	state := State{
		Version: StateVersion,
		Identity: Identity{
			DeviceUUID: uuid,
			DeviceName: cfg.DeviceName,
			Metadata: MacMetadata{
				Version:     MacMetadataVersion,
				Platform:    "macOS",
				AppVersion:  cfg.AppVersion,
				OSVersion:   cfg.OSVersion,
				DeviceModel: cfg.DeviceModel,
			},
		},
	}
	if err := translate(privatejson.WriteInitial(path, state)); err != nil {
		return nil, err
	}
	return &Store{path: path}, nil
}

// Open loads an existing state file after validating its schema and
// permissions. It never searches for another file or profile.
func Open(path string) (*Store, error) {
	path, err := validatePath(path)
	if err != nil {
		return nil, err
	}
	if _, err := load(path); err != nil {
		return nil, err
	}
	return &Store{path: path}, nil
}

// Snapshot returns a validated copy of the current state.
func (s *Store) Snapshot() (State, error) {
	if s == nil || s.path == "" {
		return State{}, ErrInvalidPath
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := load(s.path)
	if err != nil {
		return State{}, err
	}
	return cloneState(state), nil
}

// InstallCredentials atomically installs one complete server-issued result.
// Invalid or incomplete input leaves the existing file untouched.
func (s *Store) InstallCredentials(c Credentials) error {
	if s == nil || s.path == "" {
		return ErrInvalidPath
	}
	if err := validateCredentials(c); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := load(s.path)
	if err != nil {
		return err
	}
	copy := c.Clone()
	state.Credentials = &copy
	return translate(privatejson.WriteAtomic(s.path, state))
}

// CompareAndSwapCredentials atomically replaces one exact credential snapshot.
// It is intended for token rotation while the caller holds the profile owner
// lease. A stale expected value fails without modifying the state file.
func (s *Store) CompareAndSwapCredentials(expected, replacement Credentials) error {
	if s == nil || s.path == "" {
		return ErrInvalidPath
	}
	if err := validateCredentials(expected); err != nil {
		return err
	}
	if err := validateCredentials(replacement); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := load(s.path)
	if err != nil {
		return err
	}
	if state.Credentials == nil || !sameCredentials(*state.Credentials, expected) {
		return ErrCredentialsChanged
	}
	copy := replacement.Clone()
	state.Credentials = &copy
	return translate(privatejson.WriteAtomic(s.path, state))
}

// HasCredentials reports whether a complete credential set is installed.
func (s *Store) HasCredentials() (bool, error) {
	state, err := s.Snapshot()
	if err != nil {
		return false, err
	}
	return state.Credentials != nil, nil
}

func validatePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return "", ErrInvalidPath
	}
	clean := filepath.Clean(path)
	if clean == string(filepath.Separator) || filepath.Base(clean) == "." || filepath.Base(clean) == ".." {
		return "", ErrInvalidPath
	}
	return clean, nil
}

func validateConfig(cfg Config) error {
	for _, value := range []string{cfg.DeviceName, cfg.AppVersion, cfg.OSVersion, cfg.DeviceModel} {
		if strings.TrimSpace(value) == "" || !utf8.ValidString(value) || len(value) > 256 {
			return ErrInvalidConfig
		}
	}
	return nil
}

func validateCredentials(c Credentials) error {
	if c.UserID <= 0 || strings.TrimSpace(c.AccessToken) == "" || len(c.AutoLoginMaterial) == 0 ||
		!utf8.ValidString(c.AccessToken) {
		return ErrIncompleteCredentials
	}
	if len(c.AccessToken) > 16384 || len(c.AutoLoginMaterial) > 1<<20 {
		return ErrInvalidCredentials
	}
	return nil
}

func sameCredentials(a, b Credentials) bool {
	return a.UserID == b.UserID && a.AccessToken == b.AccessToken &&
		bytes.Equal(a.AutoLoginMaterial, b.AutoLoginMaterial)
}

func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ErrCorrupt
	}
	return translate(privatejson.ValidatePrivateDir(dir))
}

// load validates the private directory and file and decodes the state.
func load(path string) (State, error) {
	if err := translate(privatejson.ValidatePrivateDir(filepath.Dir(path))); err != nil {
		return State{}, err
	}
	state, found, err := privatejson.Read[State](path)
	if err != nil {
		return State{}, translate(err)
	}
	if !found {
		return State{}, ErrNotFound
	}
	if state.Version != StateVersion {
		return State{}, ErrVersionMismatch
	}
	if err := validateIdentity(state.Identity); err != nil {
		return State{}, ErrCorrupt
	}
	if state.Credentials != nil {
		if err := validateCredentials(*state.Credentials); err != nil {
			return State{}, ErrCorrupt
		}
	}
	return state, nil
}

// translate maps privatejson sentinels onto this package's errors.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, privatejson.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, privatejson.ErrAlreadyExists):
		return ErrAlreadyExists
	case errors.Is(err, privatejson.ErrUnsafePermissions):
		return ErrUnsafePermissions
	default:
		return ErrCorrupt
	}
}

func validateIdentity(id Identity) error {
	if !validUUID(id.DeviceUUID) || strings.TrimSpace(id.DeviceName) == "" || !utf8.ValidString(id.DeviceName) || len(id.DeviceName) > 256 {
		return ErrInvalidConfig
	}
	if id.Metadata.Version != MacMetadataVersion || id.Metadata.Platform != "macOS" ||
		strings.TrimSpace(id.Metadata.AppVersion) == "" || strings.TrimSpace(id.Metadata.OSVersion) == "" || strings.TrimSpace(id.Metadata.DeviceModel) == "" {
		return ErrInvalidConfig
	}
	return nil
}

func cloneState(state State) State {
	if state.Credentials != nil {
		copy := state.Credentials.Clone()
		state.Credentials = &copy
	}
	return state
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], b[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], b[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], b[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], b[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], b[10:16])
	return string(encoded), nil
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	var raw [16]byte
	n, err := hex.Decode(raw[:], []byte(strings.ReplaceAll(value, "-", "")))
	return err == nil && n == 16 && (raw[6]&0xf0) == 0x40 && (raw[8]&0xc0) == 0x80
}
