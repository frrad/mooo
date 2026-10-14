package client

import (
	"context"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
)

// ChatInfo fetches one room's chat data with CHATINFO. It sends exactly once
// and does not follow a positive LinkID with INFOLINK: OpenChat link
// resolution is left to the caller.
func (s *session) ChatInfo(ctx context.Context, chatID int64) (chatmeta.ChatInfoResponse, error) {
	return call(s, ctx, chatmeta.ChatInfoCommand, chatmeta.ChatInfoRequest{ChatID: chatID}, func(body []byte) (chatmeta.ChatInfoResponse, error) {
		response, err := chatmeta.DecodeChatInfoResponse(body)
		if err == nil && response.ChatData.ChatID != chatID {
			return response, chatmeta.ErrInvalidResponse
		}
		return response, err
	})
}

// Members resolves member profiles with MEMBER, following the official
// client: IDs below 1 and the logged-in account's own ID are dropped, nothing
// is sent when no ID remains, and the rest go in batches of
// chatmeta.MaxMemberBatch. The first failed batch ends the call without
// retrying; members from batches that already succeeded are returned with the
// error. Duplicate IDs are passed through, as in the official client.
func (s *session) Members(ctx context.Context, chatID int64, userIDs []int64) ([]chatmeta.Member, error) {
	if s == nil || ctx == nil {
		return nil, ErrProtocol
	}
	if chatID <= 0 {
		return nil, chatmeta.ErrInvalidRequest
	}
	ids := make([]int64, 0, len(userIDs))
	for _, id := range userIDs {
		if id >= 1 && id != s.userID {
			ids = append(ids, id)
		}
	}
	var members []chatmeta.Member
	for len(ids) > 0 {
		batch := ids[:min(len(ids), chatmeta.MaxMemberBatch)]
		ids = ids[len(batch):]
		response, err := call(s, ctx, chatmeta.MemberCommand, chatmeta.MemberRequest{ChatID: chatID, MemberIDs: batch}, func(body []byte) (chatmeta.MemberResponse, error) {
			response, err := chatmeta.DecodeMemberResponse(body)
			if err == nil && response.ChatID != chatID {
				return response, chatmeta.ErrInvalidResponse
			}
			return response, err
		})
		if err != nil {
			return members, err
		}
		if response.ChatID != chatID {
			return members, chatmeta.ErrInvalidResponse
		}
		members = append(members, response.Members...)
	}
	return members, nil
}

// MemberList refreshes a room's member-ID roster with MEMLIST. The official
// client issues it only from member-list UI; it sends exactly once.
func (s *session) MemberList(ctx context.Context, chatID, token int64) (chatmeta.MemberListResponse, error) {
	return call(s, ctx, chatmeta.MemberListCommand, chatmeta.MemberListRequest{ChatID: chatID, Token: token}, chatmeta.DecodeMemberListResponse)
}

// ChatInfo lazily connects once, then fetches one room's chat data.
func (c *Client) ChatInfo(ctx context.Context, chatID int64) (chatmeta.ChatInfoResponse, error) {
	return withSession(c, ctx, func(session *session) (chatmeta.ChatInfoResponse, error) {
		return session.ChatInfo(ctx, chatID)
	})
}

// Members lazily connects once, then resolves member profiles in batches
// without retrying a failed batch.
func (c *Client) Members(ctx context.Context, chatID int64, userIDs []int64) ([]chatmeta.Member, error) {
	return withSession(c, ctx, func(session *session) ([]chatmeta.Member, error) {
		return session.Members(ctx, chatID, userIDs)
	})
}

// MemberList lazily connects once, then refreshes a room's member-ID roster.
func (c *Client) MemberList(ctx context.Context, chatID, token int64) (chatmeta.MemberListResponse, error) {
	return withSession(c, ctx, func(session *session) (chatmeta.MemberListResponse, error) {
		return session.MemberList(ctx, chatID, token)
	})
}

// MoimMeta fetches the current Boards announcement, independently of CHATINFO.
func (s *session) MoimMeta(ctx context.Context, chatID int64) (chatmeta.MoimResponse, error) {
	return call(s, ctx, chatmeta.GetMoimMetaCommand, chatmeta.MoimRequest{ChatID: chatID}, chatmeta.DecodeMoimResponse)
}

func (c *Client) MoimMeta(ctx context.Context, chatID int64) (chatmeta.MoimResponse, error) {
	return withSession(c, ctx, func(session *session) (chatmeta.MoimResponse, error) {
		return session.MoimMeta(ctx, chatID)
	})
}

// PersonalMeta reads the account-wide personal settings once and selects only
// the caller's room. An omitted room returns nil, rather than a fabricated clear.
func (s *session) PersonalMeta(ctx context.Context, chatID int64) (*chatmeta.RoomMeta, error) {
	if chatID <= 0 {
		return nil, chatmeta.ErrInvalidRequest
	}
	response, err := call(s, ctx, chatmeta.PersonalMetaCommand, chatmeta.PersonalMetaRequest{}, chatmeta.DecodePersonalMetaResponse)
	if err != nil {
		return nil, err
	}
	meta, found := response.Rooms[chatID]
	if !found {
		return nil, nil
	}
	return &meta, nil
}

func (c *Client) PersonalMeta(ctx context.Context, chatID int64) (*chatmeta.RoomMeta, error) {
	if chatID <= 0 {
		return nil, chatmeta.ErrInvalidRequest
	}
	return withSession(c, ctx, func(session *session) (*chatmeta.RoomMeta, error) {
		return session.PersonalMeta(ctx, chatID)
	})
}

// ChatOnRoom sends one CHATONROOM for a chat and returns its member read
// watermarks. It sends no read acknowledgement of its own.
func (s *session) ChatOnRoom(ctx context.Context, chatID int64) (chatmeta.ChatOnRoomResponse, error) {
	return call(s, ctx, chatmeta.ChatOnRoomCommand, chatmeta.ChatOnRoomRequest{ChatID: chatID}, chatmeta.DecodeChatOnRoomResponse)
}

func (c *Client) ChatOnRoom(ctx context.Context, chatID int64) (chatmeta.ChatOnRoomResponse, error) {
	return withSession(c, ctx, func(session *session) (chatmeta.ChatOnRoomResponse, error) {
		return session.ChatOnRoom(ctx, chatID)
	})
}

// PersonalMetas reads the account's room settings once for background refresh.
func (c *Client) PersonalMetas(ctx context.Context) (map[int64]chatmeta.RoomMeta, error) {
	return withSession(c, ctx, func(s *session) (map[int64]chatmeta.RoomMeta, error) {
		response, err := call(s, ctx, chatmeta.PersonalMetaCommand, chatmeta.PersonalMetaRequest{}, chatmeta.DecodePersonalMetaResponse)
		return response.Rooms, err
	})
}
