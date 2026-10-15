package client

import (
	"context"
	"errors"
	"sort"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
)

var ErrChatListIncomplete = errors.New("client: full chat inventory unavailable")

// ListChats returns the full, typed inventory at this session's login boundary.
// OpenWithOptions with FullChatList is required for resumed profiles. It reuses
// this client's one session and does not read message history or acknowledge it.
// Accepted partial list statuses do not produce a successful complete listing.
func (c *Client) ListChats(ctx context.Context) ([]chatmeta.ChatData, error) {
	return withSession(c, ctx, func(session *session) ([]chatmeta.ChatData, error) {
		return session.ListChats()
	})
}

// ListChats decodes the completed full login inventory, never a delta snapshot.
func (s *session) ListChats() ([]chatmeta.ChatData, error) {
	if s == nil || !s.loginCursor.complete || !s.loginCursor.replaceInventory {
		return nil, ErrChatListIncomplete
	}
	result := make([]chatmeta.ChatData, 0, len(s.loginCursor.chatData))
	for _, raw := range s.loginCursor.chatData {
		data, err := chatmeta.DecodeChatData(raw)
		if err != nil {
			return nil, err
		}
		result = append(result, data)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ChatID < result[j].ChatID })
	return result, nil
}

// PersonalMetaRevision returns this session's login-list personal-metadata
// revision (mcmRevision). The official client fetches GETMCMETA only when it
// exceeds the revision it last applied. ok is false when the server sent none.
func (c *Client) PersonalMetaRevision(ctx context.Context) (int32, bool, error) {
	type result struct {
		revision int32
		ok       bool
	}
	r, err := withSession(c, ctx, func(session *session) (result, error) {
		if session == nil || session.loginCursor.mcmRevision == nil {
			return result{}, nil
		}
		return result{*session.loginCursor.mcmRevision, true}, nil
	})
	return r.revision, r.ok, err
}
