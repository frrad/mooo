package client

import (
	"context"

	"github.com/frrad/mooo/internal/protocol/chat"
)

// ModifyMessage edits one own message exactly once and returns its new
// revision. A server refusal is a StatusError; transport failure is ambiguous
// and is never retried.
func (s *session) ModifyMessage(ctx context.Context, request chat.ModifyRequest) (int64, error) {
	return call(s, ctx, chat.ModifyCommand, request, func(body []byte) (int64, error) {
		return chat.DecodeModifyResponse(body, request.LogID)
	})
}

// DeleteMessage deletes one own message for everyone exactly once. A server
// refusal is a StatusError; transport failure is ambiguous and never retried.
func (s *session) DeleteMessage(ctx context.Context, request chat.DeleteRequest) error {
	_, err := call(s, ctx, chat.DeleteCommand, request, ignoreReply)
	return err
}

// ModifyMessage edits one own message through the current session.
func (c *Client) ModifyMessage(ctx context.Context, request chat.ModifyRequest) (int64, error) {
	return withSession(c, ctx, func(session *session) (int64, error) {
		return session.ModifyMessage(ctx, request)
	})
}

// EditSource reads logID's current chat log with one GETMSGS, the original
// an edit of a sticker's text or a reply is built on.
func (s *session) EditSource(ctx context.Context, chatID, logID int64) (chat.EditSource, error) {
	return call(s, ctx, chat.GetMessagesCommand, chat.GetMessagesRequest{ChatID: chatID, LogIDs: []int64{logID}}, func(body []byte) (chat.EditSource, error) {
		return chat.DecodeEditSource(body, logID)
	})
}

// EditSource reads logID's current chat log through the current session.
func (c *Client) EditSource(ctx context.Context, chatID, logID int64) (chat.EditSource, error) {
	return withSession(c, ctx, func(session *session) (chat.EditSource, error) {
		return session.EditSource(ctx, chatID, logID)
	})
}

// DeleteMessage deletes one own message for everyone through the current session.
func (c *Client) DeleteMessage(ctx context.Context, request chat.DeleteRequest) error {
	_, err := withSession(c, ctx, func(session *session) (struct{}, error) {
		return struct{}{}, session.DeleteMessage(ctx, request)
	})
	return err
}
