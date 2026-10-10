package client

import (
	"context"

	"github.com/frrad/mooo/internal/protocol/chat"
)

// ModifyMessage edits one own message exactly once and returns its new
// revision. A server refusal is a StatusError; transport failure is ambiguous
// and is never retried.
func (s *Session) ModifyMessage(ctx context.Context, request chat.ModifyRequest) (int64, error) {
	body, err := request.MarshalBSON()
	if err != nil {
		return 0, err
	}
	if s == nil || ctx == nil {
		return 0, ErrProtocol
	}
	reply, err := s.Request(ctx, chat.ModifyCommand, body)
	if err != nil {
		return 0, err
	}
	return chat.DecodeModifyResponse(reply.Body, request.LogID)
}

// DeleteMessage deletes one own message for everyone exactly once. A server
// refusal is a StatusError; transport failure is ambiguous and never retried.
func (s *Session) DeleteMessage(ctx context.Context, request chat.DeleteRequest) error {
	body, err := request.MarshalBSON()
	if err != nil {
		return err
	}
	if s == nil || ctx == nil {
		return ErrProtocol
	}
	_, err = s.Request(ctx, chat.DeleteCommand, body)
	return err
}

// ModifyMessage edits one own message through the current session.
func (c *Client) ModifyMessage(ctx context.Context, request chat.ModifyRequest) (int64, error) {
	session, err := c.ensureSession(ctx)
	if err != nil {
		return 0, err
	}
	return session.ModifyMessage(ctx, request)
}

// DeleteMessage deletes one own message for everyone through the current session.
func (c *Client) DeleteMessage(ctx context.Context, request chat.DeleteRequest) error {
	session, err := c.ensureSession(ctx)
	if err != nil {
		return err
	}
	return session.DeleteMessage(ctx, request)
}
