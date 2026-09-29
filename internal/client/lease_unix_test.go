//go:build darwin || linux

package client

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestProfileLeaseIsExclusiveAndReleasable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json.lock")
	first, err := acquireProfileLease(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()

	if _, err := acquireProfileLease(path); !errors.Is(err, ErrProfileInUse) {
		t.Fatalf("second acquire = %v, want ErrProfileInUse", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := acquireProfileLease(path)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}
