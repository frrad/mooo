package client

import (
	"context"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
)

// ChatInfo fetches one room's chat data with CHATINFO. It sends exactly once
// and does not follow a positive LinkID with INFOLINK: OpenChat link
// resolution is left to the caller.
func (s *Session) ChatInfo(ctx context.Context, chatID int64) (chatmeta.ChatInfoResponse, error) {
	body, err := chatmeta.ChatInfoRequest{ChatID: chatID}.MarshalBSON()
	if err != nil {
		return chatmeta.ChatInfoResponse{}, err
	}
	reply, err := s.Request(ctx, chatmeta.ChatInfoCommand, body)
	if err != nil {
		return chatmeta.ChatInfoResponse{}, err
	}
	return chatmeta.DecodeChatInfoResponse(reply.Body)
}

// Members resolves member profiles with MEMBER, following the official
// client: IDs below 1 and the logged-in account's own ID are dropped, nothing
// is sent when no ID remains, and the rest go in batches of
// chatmeta.MaxMemberBatch. The first failed batch ends the call without
// retrying; members from batches that already succeeded are returned with the
// error. Duplicate IDs are passed through, as in the official client.
func (s *Session) Members(ctx context.Context, chatID int64, userIDs []int64) ([]chatmeta.Member, error) {
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
		body, err := chatmeta.MemberRequest{ChatID: chatID, MemberIDs: batch}.MarshalBSON()
		if err != nil {
			return members, err
		}
		reply, err := s.Request(ctx, chatmeta.MemberCommand, body)
		if err != nil {
			return members, err
		}
		response, err := chatmeta.DecodeMemberResponse(reply.Body)
		if err != nil {
			return members, err
		}
		members = append(members, response.Members...)
	}
	return members, nil
}

// MemberList refreshes a room's member-ID roster with MEMLIST. The official
// client issues it only from member-list UI; it sends exactly once.
func (s *Session) MemberList(ctx context.Context, chatID, token int64) (chatmeta.MemberListResponse, error) {
	body, err := chatmeta.MemberListRequest{ChatID: chatID, Token: token}.MarshalBSON()
	if err != nil {
		return chatmeta.MemberListResponse{}, err
	}
	reply, err := s.Request(ctx, chatmeta.MemberListCommand, body)
	if err != nil {
		return chatmeta.MemberListResponse{}, err
	}
	return chatmeta.DecodeMemberListResponse(reply.Body)
}

// ChatInfo lazily connects once, then fetches one room's chat data.
func (c *Client) ChatInfo(ctx context.Context, chatID int64) (chatmeta.ChatInfoResponse, error) {
	session, err := c.ensureSession(ctx)
	if err != nil {
		return chatmeta.ChatInfoResponse{}, err
	}
	return session.ChatInfo(ctx, chatID)
}

// Members lazily connects once, then resolves member profiles in batches
// without retrying a failed batch.
func (c *Client) Members(ctx context.Context, chatID int64, userIDs []int64) ([]chatmeta.Member, error) {
	session, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	return session.Members(ctx, chatID, userIDs)
}

// MemberList lazily connects once, then refreshes a room's member-ID roster.
func (c *Client) MemberList(ctx context.Context, chatID, token int64) (chatmeta.MemberListResponse, error) {
	session, err := c.ensureSession(ctx)
	if err != nil {
		return chatmeta.MemberListResponse{}, err
	}
	return session.MemberList(ctx, chatID, token)
}

// MoimMeta fetches the current Boards announcement, independently of CHATINFO.
func (s *Session) MoimMeta(ctx context.Context, chatID int64) (chatmeta.MoimResponse, error) {
	body, err := (chatmeta.MoimRequest{ChatID: chatID}).MarshalBSON()
	if err != nil {
		return chatmeta.MoimResponse{}, err
	}
	reply, err := s.Request(ctx, chatmeta.GetMoimMetaCommand, body)
	if err != nil {
		return chatmeta.MoimResponse{}, err
	}
	return chatmeta.DecodeMoimResponse(reply.Body)
}
func (c *Client) MoimMeta(ctx context.Context, chatID int64) (chatmeta.MoimResponse, error) {
	s, err := c.ensureSession(ctx)
	if err != nil {
		return chatmeta.MoimResponse{}, err
	}
	return s.MoimMeta(ctx, chatID)
}

// PersonalMeta reads the account-wide personal settings once and selects only
// the caller's room. An omitted room returns nil, rather than a fabricated clear.
func (s *Session) PersonalMeta(ctx context.Context, chatID int64) (*chatmeta.RoomMeta, error) {
	if chatID <= 0 {
		return nil, chatmeta.ErrInvalidRequest
	}
	body, err := (chatmeta.PersonalMetaRequest{}).MarshalBSON()
	if err != nil {
		return nil, err
	}
	reply, err := s.Request(ctx, chatmeta.PersonalMetaCommand, body)
	if err != nil {
		return nil, err
	}
	response, err := chatmeta.DecodePersonalMetaResponse(reply.Body)
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
	session, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	return session.PersonalMeta(ctx, chatID)
}
