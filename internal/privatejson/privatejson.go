// Package privatejson persists one JSON document per owner-only file.
//
// Every file lives in a directory with mode 0700 and has mode 0600. Neither may
// be a symlink. Replacement writes a complete, synced temporary file and
// renames it over the target, then syncs the directory, so readers never see a
// partial document.
//
// Two policies are fixed for every caller:
//
//   - Encoding never escapes HTML characters (json.Encoder.SetEscapeHTML(false)).
//     The files are never embedded in HTML, and escaping would only change how
//     strings containing <, > or & are spelled on disk.
//   - A missing file is not an error for Read: it returns found == false and a
//     nil error. ValidatePrivateDir and ValidatePrivateFile report a missing
//     path as ErrNotFound. Callers translate these sentinels into their own.
package privatejson

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// MaxSize bounds how many bytes Read decodes from one file.
const MaxSize = 2 << 20

var (
	ErrNotFound          = errors.New("privatejson: not found")
	ErrAlreadyExists     = errors.New("privatejson: already exists")
	ErrCorrupt           = errors.New("privatejson: corrupt file")
	ErrUnsafePermissions = errors.New("privatejson: unsafe permissions")
)

// ValidatePrivateDir requires dir to be a real directory with mode 0700.
func ValidatePrivateDir(dir string) error {
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

// ValidatePrivateFile requires path to be a regular file with mode 0600.
func ValidatePrivateFile(path string) error {
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

// Read validates the file's mode and decodes exactly one JSON document of at
// most MaxSize bytes with unknown fields rejected. A missing file returns the
// zero value, false and a nil error.
func Read[T any](path string) (T, bool, error) {
	var zero T
	if err := ValidatePrivateFile(path); err != nil {
		if errors.Is(err, ErrNotFound) {
			return zero, false, nil
		}
		return zero, false, err
	}
	f, err := os.Open(path)
	if err != nil {
		return zero, false, ErrCorrupt
	}
	defer func() { _ = f.Close() }()
	var value T
	decoder := json.NewDecoder(io.LimitReader(f, MaxSize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return zero, false, ErrCorrupt
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return zero, false, ErrCorrupt
	}
	return value, true, nil
}

// WriteAtomic replaces path with v. It writes a temporary file in the same
// private directory, syncs and closes it, renames it over path, reasserts mode
// 0600 and syncs the directory. On failure before the rename the temporary
// file is removed and path is untouched.
func WriteAtomic[T any](path string, v T) error {
	dir := filepath.Dir(path)
	if err := ValidatePrivateDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
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
	if err := encode(tmp, v); err != nil {
		return err
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
	return syncDirectory(dir)
}

// WriteInitial creates path with v using O_EXCL, so concurrent creators cannot
// replace one another's document. An existing path returns ErrAlreadyExists.
// On failure the partially written file is removed.
func WriteInitial[T any](path string, v T) error {
	dir := filepath.Dir(path)
	if err := ValidatePrivateDir(dir); err != nil {
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
	if err := encode(f, v); err != nil {
		return err
	}
	remove = false
	return syncDirectory(dir)
}

// encode writes v, syncs the file and closes it.
func encode[T any](f *os.File, v T) error {
	encoder := json.NewEncoder(f)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return ErrCorrupt
	}
	if err := f.Sync(); err != nil {
		return ErrCorrupt
	}
	if err := f.Close(); err != nil {
		return ErrCorrupt
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
