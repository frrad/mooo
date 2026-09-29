package client

import (
	"context"
	"errors"
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
