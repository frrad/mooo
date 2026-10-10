package privatejson

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type document struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func privateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "profile")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestReadMissingFileReportsNotFound(t *testing.T) {
	path := filepath.Join(privateDir(t), "state.json")
	value, found, err := Read[document](path)
	if err != nil || found || value != (document{}) {
		t.Fatalf("Read(missing) = %+v, %v, %v; want zero, false, nil", value, found, err)
	}
	if err := ValidatePrivateFile(path); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ValidatePrivateFile(missing) = %v, want ErrNotFound", err)
	}
	if err := ValidatePrivateDir(filepath.Join(t.TempDir(), "absent")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ValidatePrivateDir(missing) = %v, want ErrNotFound", err)
	}
}

func TestWriteInitialCreatesOwnerOnlyFileOnce(t *testing.T) {
	dir := privateDir(t)
	path := filepath.Join(dir, "state.json")
	if err := WriteInitial(path, document{Name: "first", Count: 1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, want 0600", info.Mode().Perm())
	}
	if err := WriteInitial(path, document{Name: "second", Count: 2}); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("second WriteInitial = %v, want ErrAlreadyExists", err)
	}
	value, found, err := Read[document](path)
	if err != nil || !found || value != (document{Name: "first", Count: 1}) {
		t.Fatalf("Read = %+v, %v, %v; want the first document", value, found, err)
	}
}

func TestWriteAtomicReplacesWithoutLeavingTemporaryFiles(t *testing.T) {
	dir := privateDir(t)
	path := filepath.Join(dir, "state.json")
	if err := WriteInitial(path, document{Name: "first", Count: 1}); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(path, document{Name: "second", Count: 2}); err != nil {
		t.Fatal(err)
	}
	value, found, err := Read[document](path)
	if err != nil || !found || value != (document{Name: "second", Count: 2}) {
		t.Fatalf("Read = %+v, %v, %v; want the second document", value, found, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, want 0600", info.Mode().Perm())
	}
	if names := dirEntries(t, dir); len(names) != 1 || names[0] != "state.json" {
		t.Fatalf("directory entries = %v, want only state.json", names)
	}
}

func TestWriteAtomicEncodeFailureLeavesTargetUntouched(t *testing.T) {
	dir := privateDir(t)
	path := filepath.Join(dir, "state.json")
	if err := WriteInitial(path, document{Name: "kept", Count: 1}); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(path, map[string]any{"bad": make(chan int)}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("WriteAtomic(unencodable) = %v, want ErrCorrupt", err)
	}
	value, found, err := Read[document](path)
	if err != nil || !found || value != (document{Name: "kept", Count: 1}) {
		t.Fatalf("Read = %+v, %v, %v; want the original document", value, found, err)
	}
	if names := dirEntries(t, dir); len(names) != 1 || names[0] != "state.json" {
		t.Fatalf("directory entries = %v, want only state.json", names)
	}
}

func TestWriteInitialEncodeFailureRemovesFile(t *testing.T) {
	dir := privateDir(t)
	path := filepath.Join(dir, "state.json")
	if err := WriteInitial(path, map[string]any{"bad": make(chan int)}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("WriteInitial(unencodable) = %v, want ErrCorrupt", err)
	}
	if names := dirEntries(t, dir); len(names) != 0 {
		t.Fatalf("directory entries = %v, want none", names)
	}
}

func TestEncodingDoesNotEscapeHTML(t *testing.T) {
	path := filepath.Join(privateDir(t), "state.json")
	if err := WriteInitial(path, document{Name: "<a&b>", Count: 1}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{\"name\":\"<a&b>\",\"count\":1}\n" {
		t.Fatalf("file contents = %q", raw)
	}
}

func TestReadRejectsMalformedDocuments(t *testing.T) {
	for name, contents := range map[string]string{
		"unknown field": `{"name":"x","count":1,"extra":true}`,
		"trailing data": `{"name":"x","count":1} {}`,
		"truncated":     `{"name":"x"`,
		"oversize":      `{"name":"` + strings.Repeat("x", MaxSize) + `","count":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(privateDir(t), "state.json")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Read[document](path); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("Read = %v, want ErrCorrupt", err)
			}
		})
	}
}

func TestUnsafeModesAndSymlinksAreRejected(t *testing.T) {
	dir := privateDir(t)
	path := filepath.Join(dir, "state.json")
	if err := WriteInitial(path, document{Name: "x", Count: 1}); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Read[document](path); !errors.Is(err, ErrUnsafePermissions) {
		t.Fatalf("Read(0644 file) = %v, want ErrUnsafePermissions", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Read[document](link); !errors.Is(err, ErrUnsafePermissions) {
		t.Fatalf("Read(symlink) = %v, want ErrUnsafePermissions", err)
	}

	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if err := WriteAtomic(path, document{Name: "y", Count: 2}); !errors.Is(err, ErrUnsafePermissions) {
		t.Fatalf("WriteAtomic(0755 dir) = %v, want ErrUnsafePermissions", err)
	}
	if err := WriteInitial(filepath.Join(dir, "other.json"), document{}); !errors.Is(err, ErrUnsafePermissions) {
		t.Fatalf("WriteInitial(0755 dir) = %v, want ErrUnsafePermissions", err)
	}

	linkedDir := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(dir, linkedDir); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePrivateDir(linkedDir); !errors.Is(err, ErrUnsafePermissions) {
		t.Fatalf("ValidatePrivateDir(symlink) = %v, want ErrUnsafePermissions", err)
	}
}
