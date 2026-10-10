package client

import (
	"context"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/friends"
	"github.com/frrad/mooo/internal/protocol/macweb"
)

// addFriendByPhone resolves a phone number through Kakao's authenticated friend
// mutation and returns the server-assigned user ID. The HTTP operation is sent
// exactly once and is not retried when its outcome is ambiguous.
func addFriendByPhone(ctx context.Context, doer macweb.Doer, state authstate.State, request friends.AddByPhoneRequest) (friends.Friend, error) {
	profile, err := webProfile(state)
	if err != nil {
		return friends.Friend{}, err
	}
	response, err := friends.AddByPhone(ctx, doer, profile, request)
	if err != nil {
		// Preserve any structured friend data returned alongside a status error;
		// callers may use it to reconcile an already-applied mutation.
		return response.Friend, err
	}
	return response.Friend, nil
}
