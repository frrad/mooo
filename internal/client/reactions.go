package client

import (
	"context"
	"sync"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/macweb"
	"github.com/frrad/mooo/internal/protocol/reactions"
)

func sendReaction(ctx context.Context, doer macweb.Doer, state authstate.State, request reactions.Request) (reactions.Response, error) {
	profile, err := webProfile(state)
	if err != nil {
		return reactions.Response{}, err
	}
	return reactions.Send(ctx, doer, profile, request)
}

func reactionMembers(ctx context.Context, doer macweb.Doer, state authstate.State, chatID, logID int64) (reactions.MembersResponse, error) {
	profile, err := webProfile(state)
	if err != nil {
		return reactions.MembersResponse{}, err
	}
	return reactions.FetchMembers(ctx, doer, profile, chatID, logID)
}

func miniReactionDetails(ctx context.Context, doer macweb.Doer, state authstate.State, chatID, linkID, logID int64) (reactions.DetailsResponse, error) {
	profile, err := webProfile(state)
	if err != nil {
		return reactions.DetailsResponse{}, err
	}
	return reactions.FetchDetails(ctx, doer, profile, chatID, linkID, logID)
}

func mergedReactionDetails(ctx context.Context, doer macweb.Doer, state authstate.State, change events.ReactionChanged) ([]reactions.Detail, error) {
	if ctx == nil || change.ChatID <= 0 || change.LinkID < 0 || change.LogID <= 0 {
		return nil, ErrProtocol
	}
	profile, err := webProfile(state)
	if err != nil {
		return nil, err
	}
	var needLegacy, needMini bool
	for _, item := range change.Items {
		switch item.Kind {
		case 1:
			needLegacy = true
		case 2:
			needMini = true
		}
	}
	if !needLegacy && !needMini {
		return []reactions.Detail{}, nil
	}
	var members reactions.MembersResponse
	var mini reactions.DetailsResponse
	var membersErr, miniErr error
	var wait sync.WaitGroup
	if needLegacy {
		wait.Add(1)
		go func() {
			defer wait.Done()
			members, membersErr = reactions.FetchMembers(ctx, doer, profile, change.ChatID, change.LogID)
		}()
	}
	if needMini {
		wait.Add(1)
		go func() {
			defer wait.Done()
			mini, miniErr = reactions.FetchDetails(ctx, doer, profile, change.ChatID, change.LinkID, change.LogID)
		}()
	}
	wait.Wait()
	if membersErr != nil {
		return nil, membersErr
	}
	if miniErr != nil {
		return nil, miniErr
	}
	return reactions.MergeDetails(members, mini), nil
}

// webProfile is the authenticated Mac web API profile for every HTTP endpoint
// the client calls.
func webProfile(state authstate.State) (macweb.Profile, error) {
	if state.Credentials == nil {
		return macweb.Profile{}, ErrCredentialsAbsent
	}
	wireUUID, err := state.Identity.WireDeviceUUID()
	if err != nil {
		return macweb.Profile{}, ErrBootstrap
	}
	return macweb.Profile{
		AppVersion: state.Identity.Metadata.AppVersion, OSVersion: state.Identity.Metadata.OSVersion,
		Language: "en", UserID: state.Credentials.UserID,
		AccessToken: state.Credentials.AccessToken, DeviceUUID: wireUUID,
	}, nil
}
