package client

import (
	"context"

	"github.com/frrad/mooo/internal/protocol/chat"
)

// AddMembers sends one invitation without retrying an ambiguous result.
func (s *Session) AddMembers(ctx context.Context, request chat.AddMembersRequest) (chat.AddMembersResponse, error) {
	body, err := request.MarshalBSON()
	if err != nil {
		return chat.AddMembersResponse{}, err
	}
	if s == nil {
		return chat.AddMembersResponse{}, ErrProtocol
	}
	reply, err := s.Request(ctx, chat.AddMembersCommand, body)
	if err != nil {
		return chat.AddMembersResponse{}, err
	}
	return chat.DecodeAddMembersResponse(reply.Body)
}

// CreateChat invokes Kakao's generic CREATE primitive. A request containing
// one member creates a direct chat; requests with multiple members create a
// group. The transport sends the mutation exactly once and never retries an
// ambiguous result.
func (s *Session) CreateChat(ctx context.Context, request chat.CreateRequest) (chat.CreateResponse, error) {
	body, err := request.MarshalBSON()
	if err != nil {
		return chat.CreateResponse{}, err
	}
	reply, err := s.Request(ctx, chat.CreateCommand, body)
	if err != nil {
		return chat.CreateResponse{}, err
	}
	return chat.DecodeCreateResponse(reply.Body)
}

// SendText sends one text message exactly once using the current direct-text
// WRITE shape.
func (s *Session) SendText(ctx context.Context, chatID int64, message string) (chat.WriteResponse, error) {
	request := chat.WriteRequest{
		ChatID: chatID, Message: message, Type: chat.TextType,
	}
	if err := request.Validate(); err != nil {
		return chat.WriteResponse{}, err
	}
	if s == nil {
		return chat.WriteResponse{}, ErrProtocol
	}
	body, err := request.MarshalBSON()
	if err != nil {
		return chat.WriteResponse{}, err
	}
	reply, err := s.Request(ctx, chat.WriteCommand, body)
	if err != nil {
		return chat.WriteResponse{}, err
	}
	return chat.DecodeWriteResponse(reply.Body)
}

// SendReply sends one type-26 reply exactly once. The target metadata is
// serialized independently of UI state and the mutation is never retried.
func (s *Session) SendReply(ctx context.Context, request chat.ReplyRequest) (chat.WriteResponse, error) {
	body, err := request.MarshalBSON()
	if err != nil {
		return chat.WriteResponse{}, err
	}
	if s == nil {
		return chat.WriteResponse{}, ErrProtocol
	}
	reply, err := s.Request(ctx, chat.WriteCommand, body)
	if err != nil {
		return chat.WriteResponse{}, err
	}
	return chat.DecodeWriteResponse(reply.Body)
}
