package command

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/client"
)

type fakeContactPhotoClient struct {
	data       []byte
	err        error
	closed     int
	chat, user int64
}

func (f *fakeContactPhotoClient) ContactProfilePhoto(_ context.Context, chat, user int64, _ *http.Client) ([]byte, error) {
	f.chat, f.user = chat, user
	return f.data, f.err
}
func (f *fakeContactPhotoClient) Shutdown(context.Context) error { f.closed++; return nil }

func TestContactPhotoCLIUsesSelectedContactAndPrivateOutput(t *testing.T) {
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	out := filepath.Join(root, "photo.jpg")
	state := filepath.Join(root, "profile")
	fake := &fakeContactPhotoClient{data: []byte("synthetic validated image")}
	var stdout bytes.Buffer
	runErr := runContactPhoto([]string{"--state", state, "--chat", "42", "--user", "7", "--output", out}, &stdout, func(path string) (contactPhotoClient, error) {
		if path != state {
			t.Error("wrong selected profile")
		}
		return fake, nil
	})
	data, err := os.ReadFile(out)
	stat, statErr := os.Stat(out)
	if runErr != nil || err != nil || statErr != nil || !bytes.Equal(data, fake.data) || stat.Mode().Perm() != 0600 || fake.closed != 1 || fake.chat != 42 || fake.user != 7 {
		t.Fatalf("run=%v error=%v/%v closes=%d", runErr, err, statErr, fake.closed)
	}
	if strings.Contains(stdout.String(), state) || strings.Contains(stdout.String(), "synthetic") {
		t.Fatal("private output leaked")
	}
}

func TestContactPhotoCLIAbsentPhotoRemovesOutputAndClosesProfile(t *testing.T) {
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	out := filepath.Join(root, "photo.jpg")
	fake := &fakeContactPhotoClient{err: client.ErrContactPhotoAbsent}
	var stdout bytes.Buffer
	runErr := runContactPhoto([]string{"--state", filepath.Join(root, "profile"), "--chat", "42", "--user", "7", "--output", out}, &stdout, func(string) (contactPhotoClient, error) { return fake, nil })
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) || runErr == nil || fake.closed != 1 {
		t.Fatalf("run=%v err=%v closes=%d", runErr, err, fake.closed)
	}
}

func TestContactPhotoCLIRejectsUnsafeDestinationBeforeOpeningProfile(t *testing.T) {
	root := t.TempDir()
	_ = os.Chmod(root, 0755)
	var stdout bytes.Buffer
	runErr := runContactPhoto([]string{"--state", filepath.Join(root, "profile"), "--chat", "42", "--user", "7", "--output", filepath.Join(root, "photo")}, &stdout, func(string) (contactPhotoClient, error) {
		t.Error("unsafe destination authenticated")
		return nil, errors.New("unused")
	})
	if runErr == nil {
		t.Fatal("unsafe destination succeeded")
	}
}
