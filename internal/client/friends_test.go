package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/friends"
)

func TestAddFriendByPhoneRequiresCredentials(t *testing.T) {
	_, err := addFriendByPhone(context.Background(), nil, authstate.State{}, friends.AddByPhoneRequest{})
	if !errors.Is(err, ErrCredentialsAbsent) {
		t.Fatalf("AddFriendByPhone error = %v, want ErrCredentialsAbsent", err)
	}
}

type friendDoerFunc func(*http.Request) (*http.Response, error)

func (f friendDoerFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }

func TestAddFriendByPhonePreservesStructuredErrorFriend(t *testing.T) {
	state := authstate.State{
		Identity: authstate.Identity{
			DeviceUUID: "123e4567-e89b-42d3-a456-426614174000",
			Metadata:   authstate.MacMetadata{AppVersion: "26.8.0", OSVersion: "26.6.2"},
		},
		Credentials: &authstate.Credentials{UserID: 1, AccessToken: "token"},
	}
	doer := friendDoerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"status":1101,"friend":{"userId":7}}`)),
		}, nil
	})
	friend, err := addFriendByPhone(context.Background(), doer, state, friends.AddByPhoneRequest{
		PhoneNumber: "5550100", CountryISO: "US", CountryCode: "1",
	})
	if !errors.Is(err, friends.ErrRejected) || friend.UserID != 7 {
		t.Fatalf("AddFriendByPhone = (%d, %v), want (7, ErrRejected)", friend.UserID, err)
	}
}
