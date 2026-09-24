// Package authstate stores the identity and authentication result owned by the
// clean-room secondary-device client.
//
// It deliberately accepts an explicit state-file path. It does not discover,
// inspect, or import any official KakaoTalk application container.
package authstate

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"
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
	if err := writeInitial(path, state); err != nil {
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
	if err := validatePrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err := validatePrivateFile(path); err != nil {
		return nil, err
	}
	if _, err := read(path); err != nil {
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
	if err := validatePrivateDir(filepath.Dir(s.path)); err != nil {
		return State{}, err
	}
	if err := validatePrivateFile(s.path); err != nil {
		return State{}, err
	}
	state, err := read(s.path)
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
	if err := validatePrivateDir(filepath.Dir(s.path)); err != nil {
		return err
	}
	if err := validatePrivateFile(s.path); err != nil {
		return err
	}
	state, err := read(s.path)
	if err != nil {
		return err
	}
	copy := c.Clone()
	state.Credentials = &copy
	return writeAtomic(s.path, state)
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

func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ErrCorrupt
	}
	return validatePrivateDir(dir)
}

func validatePrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return ErrCorrupt
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return ErrUnsafePermissions
	}
	return nil
}

func validatePrivateFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return ErrCorrupt
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return ErrUnsafePermissions
	}
	return nil
}

func read(path string) (State, error) {
	f, err := os.Open(path)
	if err != nil {
		return State{}, ErrCorrupt
	}
	defer func() { _ = f.Close() }()
	var state State
	decoder := json.NewDecoder(io.LimitReader(f, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return State{}, ErrCorrupt
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return State{}, ErrCorrupt
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

func writeAtomic(path string, state State) error {
	dir := filepath.Dir(path)
	if err := validatePrivateDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".authstate-*")
	if err != nil {
		return ErrCorrupt
	}
	tmpName := tmp.Name()
	remove := true
	defer func() {
		_ = tmp.Close()
		if remove {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return ErrUnsafePermissions
	}
	encoder := json.NewEncoder(tmp)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(state); err != nil {
		return ErrCorrupt
	}
	if err := tmp.Sync(); err != nil {
		return ErrCorrupt
	}
	if err := tmp.Close(); err != nil {
		return ErrCorrupt
	}
	if err := os.Rename(tmpName, path); err != nil {
		return ErrCorrupt
	}
	remove = false
	// Keep the replacement owner-only even on filesystems with unusual umask
	// behavior. Rename is used only after the complete file is synced.
	if err := os.Chmod(path, 0o600); err != nil {
		return ErrUnsafePermissions
	}
	if err := syncDirectory(dir); err != nil {
		return err
	}
	return nil
}

// writeInitial uses O_EXCL so concurrent creators cannot replace one
// another's freshly generated UUID. Credential replacement uses writeAtomic.
func writeInitial(path string, state State) error {
	dir := filepath.Dir(path)
	if err := validatePrivateDir(dir); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrAlreadyExists
		}
		return ErrCorrupt
	}
	remove := true
	defer func() {
		_ = f.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		return ErrUnsafePermissions
	}
	encoder := json.NewEncoder(f)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(state); err != nil {
		return ErrCorrupt
	}
	if err := f.Sync(); err != nil {
		return ErrCorrupt
	}
	if err := f.Close(); err != nil {
		return ErrCorrupt
	}
	remove = false
	if err := syncDirectory(dir); err != nil {
		return err
	}
	return nil
}

// syncDirectory makes a completed create/rename durable on filesystems that
// support syncing directory entries. Windows does not expose this operation;
// file contents remain individually flushed there and directory syncing is
// intentionally treated as an unsupported no-op.
func syncDirectory(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return ErrCorrupt
	}
	defer func() { _ = f.Close() }()
	if err := f.Sync(); err != nil {
		return ErrCorrupt
	}
	return nil
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
