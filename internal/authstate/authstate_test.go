package authstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testConfig() Config {
	return Config{
		DeviceName:  "Mooo Lab Mac",
		AppVersion:  "26.8.0-test",
		OSVersion:   "macOS 26.0-test",
		DeviceModel: "MacBookAir-test",
	}
}

func testPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "client", "authstate.json")
}

func TestCreateGeneratesAndReusesClientOwnedIdentity(t *testing.T) {
	path := testPath(t)
	store, err := Create(path, testConfig())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	initial, err := store.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if initial.Credentials != nil {
		t.Fatal("new state unexpectedly has credentials")
	}
	if !validUUID(initial.Identity.DeviceUUID) {
		t.Fatalf("invalid generated UUID %q", initial.Identity.DeviceUUID)
	}
	if initial.Identity.DeviceName != "Mooo Lab Mac" || initial.Identity.Metadata.Platform != "macOS" {
		t.Fatalf("identity metadata not preserved: %#v", initial.Identity)
	}
	if _, err := Create(path, testConfig()); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("second Create error = %v, want ErrAlreadyExists", err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	reloaded, err := reopened.Snapshot()
	if err != nil {
		t.Fatalf("reopened Snapshot: %v", err)
	}
	if reloaded.Identity.DeviceUUID != initial.Identity.DeviceUUID {
		t.Fatalf("UUID changed across reload: %q != %q", reloaded.Identity.DeviceUUID, initial.Identity.DeviceUUID)
	}
	if has, err := reopened.HasCredentials(); err != nil || has {
		t.Fatalf("HasCredentials = %v, %v; want false, nil", has, err)
	}
}

func TestCreateRequiresExplicitAbsolutePathAndConfig(t *testing.T) {
	if _, err := Create("relative/state.json", testConfig()); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("relative path error = %v, want ErrInvalidPath", err)
	}
	for name, cfg := range map[string]Config{
		"missing name":  func() Config { c := testConfig(); c.DeviceName = ""; return c }(),
		"missing app":   func() Config { c := testConfig(); c.AppVersion = ""; return c }(),
		"missing os":    func() Config { c := testConfig(); c.OSVersion = ""; return c }(),
		"missing model": func() Config { c := testConfig(); c.DeviceModel = ""; return c }(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Create(testPath(t), cfg); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestPrivatePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission semantics are not available on Windows")
	}
	path := testPath(t)
	store, err := Create(path, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("state directory mode = %v, %v; want 0700", info.Mode().Perm(), err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Snapshot(); !errors.Is(err, ErrUnsafePermissions) {
		t.Fatalf("world-readable state error = %v, want ErrUnsafePermissions", err)
	}
}

func TestInstallCredentialsIsCompleteAndAtomic(t *testing.T) {
	path := testPath(t)
	store, err := Create(path, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bad := []Credentials{
		{UserID: 1, AccessToken: "", AutoLoginMaterial: []byte("material")},
		{UserID: 0, AccessToken: "token", AutoLoginMaterial: []byte("material")},
		{UserID: 1, AccessToken: "token", AutoLoginMaterial: nil},
	}
	for _, credentials := range bad {
		if err := store.InstallCredentials(credentials); !errors.Is(err, ErrIncompleteCredentials) {
			t.Fatalf("invalid credentials error = %v, want ErrIncompleteCredentials", err)
		}
		after, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(after) != string(before) {
			t.Fatal("invalid install modified the existing state")
		}
	}
	wanted := Credentials{UserID: 123456789, AccessToken: "synthetic-token", AutoLoginMaterial: []byte{0, 1, 2, 3}}
	if err := store.InstallCredentials(wanted); err != nil {
		t.Fatalf("InstallCredentials: %v", err)
	}
	loaded, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Credentials == nil || loaded.Credentials.UserID != wanted.UserID || loaded.Credentials.AccessToken != wanted.AccessToken || string(loaded.Credentials.AutoLoginMaterial) != string(wanted.AutoLoginMaterial) {
		t.Fatalf("installed credentials not preserved: %v", loaded.Credentials)
	}
	// Snapshot must not expose the store's backing byte slice.
	loaded.Credentials.AutoLoginMaterial[0] = 99
	again, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if again.Credentials.AutoLoginMaterial[0] != 0 {
		t.Fatal("Snapshot returned mutable backing material")
	}
}

func TestCorruptionAndVersionMismatchFailClosed(t *testing.T) {
	path := testPath(t)
	if _, err := Create(path, testConfig()); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":999}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("version mismatch error = %v, want ErrVersionMismatch", err)
	}
	if err := os.WriteFile(path, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corrupt error = %v, want ErrCorrupt", err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"identity":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("incomplete identity error = %v, want ErrCorrupt", err)
	}
}

func TestSymlinkAndUnsafeDirectoryFailClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink and Unix permission semantics are not available on Windows")
	}
	root := t.TempDir()
	targetDir := filepath.Join(root, "target")
	if err := os.Mkdir(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "link")
	if err := os.Symlink(targetDir, linkDir); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(filepath.Join(linkDir, "state.json"), testConfig()); !errors.Is(err, ErrUnsafePermissions) {
		t.Fatalf("symlink directory error = %v, want ErrUnsafePermissions", err)
	}
	// Keep the symlink probe isolated from the normal state path. The target
	// directory above is intentionally created with non-private defaults by
	// some platforms' test filesystems.
	privateRoot := t.TempDir()
	if err := os.Chmod(privateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(privateRoot, "state.json")
	if _, err := Create(path, testConfig()); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "linked.json")
	if err := os.Symlink(path, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(linked); !errors.Is(err, ErrUnsafePermissions) {
		t.Fatalf("symlink file error = %v, want ErrUnsafePermissions", err)
	}
}

func TestFormattingAndErrorsDoNotRevealSecrets(t *testing.T) {
	secret := "synthetic-secret-token-9c9f"
	credentials := Credentials{UserID: 987654321, AccessToken: secret, AutoLoginMaterial: []byte(secret)}
	if strings.Contains(fmt.Sprintf("%v %#v %+v", credentials, credentials, credentials), secret) {
		t.Fatal("credential formatting revealed a secret")
	}
	identity := Identity{DeviceUUID: "synthetic-device-id", DeviceName: "test"}
	if strings.Contains(fmt.Sprintf("%v %#v %+v", identity, identity, identity), "synthetic-device-id") || strings.Contains(fmt.Sprintf("%v %#v %+v", identity, identity, identity), "test") {
		t.Fatal("identity formatting revealed device-sensitive values")
	}
	state := State{Version: StateVersion, Identity: identity, Credentials: &credentials}
	if strings.Contains(fmt.Sprintf("%v %#v %+v", state, state, state), secret) || strings.Contains(state.String(), "synthetic-device-id") {
		t.Fatal("state formatting revealed secret or device identity")
	}
	if _, err := Create("relative/"+secret, testConfig()); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("path error revealed input: %v", err)
	}
}
