package client

import (
	"context"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/friends"
	"github.com/frrad/mooo/internal/protocol/reactions"
)

func sendReaction(ctx context.Context, doer friends.Doer, state authstate.State, request reactions.Request) (reactions.Response, error) {
	profile, err := reactionProfile(state)
	if err != nil {
		return reactions.Response{}, err
	}
	return reactions.Send(ctx, doer, profile, request)
}

func reactionMembers(ctx context.Context, doer friends.Doer, state authstate.State, chatID, logID int64) (reactions.MembersResponse, error) {
	profile, err := reactionProfile(state)
	if err != nil {
		return reactions.MembersResponse{}, err
	}
	return reactions.FetchMembers(ctx, doer, profile, chatID, logID)
}

func reactionProfile(state authstate.State) (reactions.ClientProfile, error) {
	if state.Credentials == nil {
		return reactions.ClientProfile{}, ErrCredentialsAbsent
	}
	wireUUID, err := state.Identity.WireDeviceUUID()
	if err != nil {
		return reactions.ClientProfile{}, ErrBootstrap
	}
	return reactions.ClientProfile{
		AppVersion: state.Identity.Metadata.AppVersion, OSVersion: state.Identity.Metadata.OSVersion,
		Language: "en", AccessToken: state.Credentials.AccessToken, DeviceUUID: wireUUID,
	}, nil
}
