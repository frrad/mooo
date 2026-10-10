package client

import (
	"context"

	"github.com/frrad/mooo/internal/protocol/chat"

	"github.com/frrad/mooo/internal/protocol/messagetype"
)

// AddMembers sends one invitation without retrying an ambiguous result.
func (s *session) AddMembers(ctx context.Context, request chat.AddMembersRequest) (chat.AddMembersResponse, error) {
	return call(s, ctx, chat.AddMembersCommand, request, chat.DecodeAddMembersResponse)
}

// CreateChat invokes Kakao's generic CREATE primitive. A request containing
// one member creates a direct chat; requests with multiple members create a
// group. The transport sends the mutation exactly once and never retries an
// ambiguous result.
func (s *session) CreateChat(ctx context.Context, request chat.CreateRequest) (chat.CreateResponse, error) {
	return call(s, ctx, chat.CreateCommand, request, chat.DecodeCreateResponse)
}

// SendText sends one text message exactly once using the current direct-text
// WRITE shape.
func (s *session) SendText(ctx context.Context, chatID int64, message string) (chat.WriteResponse, error) {
	request := chat.WriteRequest{
		ChatID: chatID, Message: message, Type: messagetype.Text,
	}
	if err := request.Validate(); err != nil {
		return chat.WriteResponse{}, err
	}
	return call(s, ctx, chat.WriteCommand, request, chat.DecodeWriteResponse)
}

// SendReply sends one type-26 reply exactly once. The target metadata is
// serialized independently of UI state and the mutation is never retried.
func (s *session) SendReply(ctx context.Context, request chat.ReplyRequest) (chat.WriteResponse, error) {
	return call(s, ctx, chat.WriteCommand, request, chat.DecodeWriteResponse)
}
