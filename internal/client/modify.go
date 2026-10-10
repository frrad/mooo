package client

import (
	"context"

	"github.com/frrad/mooo/internal/protocol/chat"
)

// ModifyMessage edits one own message exactly once and returns its new
// revision. A server refusal is a StatusError; transport failure is ambiguous
// and is never retried.
func (s *Session) ModifyMessage(ctx context.Context, request chat.ModifyRequest) (int64, error) {
	return call(s, ctx, chat.ModifyCommand, request, func(body []byte) (int64, error) {
		return chat.DecodeModifyResponse(body, request.LogID)
	})
}

// DeleteMessage deletes one own message for everyone exactly once. A server
// refusal is a StatusError; transport failure is ambiguous and never retried.
func (s *Session) DeleteMessage(ctx context.Context, request chat.DeleteRequest) error {
	_, err := call(s, ctx, chat.DeleteCommand, request, ignoreReply)
	return err
}

// ModifyMessage edits one own message through the current session.
func (c *Client) ModifyMessage(ctx context.Context, request chat.ModifyRequest) (int64, error) {
	return withSession(c, ctx, func(session *Session) (int64, error) {
		return session.ModifyMessage(ctx, request)
	})
}

// DeleteMessage deletes one own message for everyone through the current session.
func (c *Client) DeleteMessage(ctx context.Context, request chat.DeleteRequest) error {
	_, err := withSession(c, ctx, func(session *Session) (struct{}, error) {
		return struct{}{}, session.DeleteMessage(ctx, request)
	})
	return err
}
