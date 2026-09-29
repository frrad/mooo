package client

import (
	"context"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/friends"
)

// addFriendByPhone resolves a phone number through Kakao's authenticated friend
// mutation and returns the server-assigned user ID. The HTTP operation is sent
// exactly once and is not retried when its outcome is ambiguous.
func addFriendByPhone(ctx context.Context, doer friends.Doer, state authstate.State, request friends.AddByPhoneRequest) (friends.Friend, error) {
	if state.Credentials == nil {
		return friends.Friend{}, ErrCredentialsAbsent
	}
	wireUUID, err := state.Identity.WireDeviceUUID()
	if err != nil {
		return friends.Friend{}, ErrBootstrap
	}
	response, err := friends.AddByPhone(ctx, doer, friends.ClientProfile{
		AppVersion:  state.Identity.Metadata.AppVersion,
		OSVersion:   state.Identity.Metadata.OSVersion,
		Language:    "en",
		AccessToken: state.Credentials.AccessToken,
		DeviceUUID:  wireUUID,
	}, request)
	if err != nil {
		// Preserve any structured friend data returned alongside a status error;
		// callers may use it to reconcile an already-applied mutation.
		return response.Friend, err
	}
	return response.Friend, nil
}
