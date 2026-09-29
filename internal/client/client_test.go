package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/authstate"
)

func reusableTestState() authstate.State {
	return authstate.State{
		Identity: authstate.Identity{
			DeviceUUID: "123e4567-e89b-42d3-a456-426614174000",
			Metadata:   authstate.MacMetadata{AppVersion: "26.8.0", OSVersion: "26.6.2"},
		},
		Credentials: &authstate.Credentials{UserID: 1, AccessToken: "token"},
	}
}

func TestClientConnectRenewsOnceOnExpiredToken(t *testing.T) {
	root := t.TempDir()
	if err := setPrivateDir(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "state.json")
	store, err := authstate.Create(path, authstate.Config{
		DeviceName: "test", AppVersion: "26.8.0", OSVersion: "26.6.2", DeviceModel: "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	old := authstate.Credentials{UserID: 1, AccessToken: "old-access", AutoLoginMaterial: []byte(`{"refresh_token":"old-refresh","token_type":"bearer","display_account_id":"keep"}`)}
	if err := store.InstallCredentials(old); err != nil {
		t.Fatal(err)
	}
	state, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	doer := friendDoerFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.Header.Get("Authorization") == "" {
			t.Fatal("renewal omitted Authorization")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
			`{"access_token":"new-access","refresh_token":"new-refresh","token_type":"rotated"}`,
		))}, nil
	})
	client, err := newClient(state, doer)
	if err != nil {
		t.Fatal(err)
	}
	client.store = store
	dials := 0
	client.dial = func(_ context.Context, got authstate.State) (*Session, error) {
		dials++
		if dials == 1 {
			return nil, StatusError{Command: "LOGINLIST", Status: -950}
		}
		if got.Credentials.AccessToken != "new-access" {
			t.Fatal("second login did not use rotated access token")
		}
		return &Session{}, nil
	}
	if err := client.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || dials != 2 {
		t.Fatalf("requests=%d dials=%d, want 1 and 2", requests, dials)
	}
	persisted, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Credentials.AccessToken != "new-access" || !strings.Contains(string(persisted.Credentials.AutoLoginMaterial), `"display_account_id":"keep"`) || !strings.Contains(string(persisted.Credentials.AutoLoginMaterial), `"refresh_token":"new-refresh"`) {
		t.Fatalf("rotated state did not preserve metadata: %v", persisted.Credentials)
	}
}

func TestClientConnectDoesNotRepeatFailedRenewal(t *testing.T) {
	root := t.TempDir()
	if err := setPrivateDir(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "state.json")
	store, err := authstate.Create(path, authstate.Config{
		DeviceName: "test", AppVersion: "26.8.0", OSVersion: "26.6.2", DeviceModel: "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	old := authstate.Credentials{UserID: 1, AccessToken: "old-access", AutoLoginMaterial: []byte(`{"refresh_token":"old-refresh","token_type":"bearer"}`)}
	if err := store.InstallCredentials(old); err != nil {
		t.Fatal(err)
	}
	state, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	doer := friendDoerFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"access_token":"incomplete"}`))}, nil
	})
	client, err := newClient(state, doer)
	if err != nil {
		t.Fatal(err)
	}
	client.store = store
	client.dial = func(context.Context, authstate.State) (*Session, error) {
		return nil, StatusError{Command: "LOGINLIST", Status: -950}
	}
	if err := client.Connect(t.Context()); !errors.Is(err, ErrCredentialRenewal) {
		t.Fatalf("first Connect error = %v, want ErrCredentialRenewal", err)
	}
	if err := client.Connect(t.Context()); err == nil {
		t.Fatal("second Connect unexpectedly succeeded")
	}
	if requests != 1 {
		t.Fatalf("renewal request count = %d, want 1", requests)
	}
	persisted, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Credentials.AccessToken != old.AccessToken || string(persisted.Credentials.AutoLoginMaterial) != string(old.AutoLoginMaterial) {
		t.Fatal("failed renewal modified persisted credentials")
	}
}

func setPrivateDir(path string) error {
	return os.Chmod(path, 0o700)
}

func TestClientConnectReusesOneSession(t *testing.T) {
	client, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	connects := 0
	client.dial = func(context.Context, authstate.State) (*Session, error) {
		connects++
		return &Session{}, nil
	}
	if err := client.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	if connects != 1 {
		t.Fatalf("dial count = %d, want 1", connects)
	}
}

func TestClientClosePreventsReconnect(t *testing.T) {
	client, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(t.Context()); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("Connect after Close = %v, want ErrClientClosed", err)
	}
}
