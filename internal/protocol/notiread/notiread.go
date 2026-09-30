// Package notiread implements the automatic inbound-message notification-read
// acknowledgement. It deliberately does not interpret the response: the
// official response/completion semantics remain under review.
package notiread

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const Command = "NOTIREAD"

var ErrInvalidRequest = errors.New("notiread: invalid request")

// Request is the five-field NOTIREAD request sent after an accepted inbound
// message for an existing room. LinkID may be zero for direct chats.
type Request struct {
	ChatID    int64
	LinkID    int64
	Watermark int64
	NotiRead  bool
	ServiceID int32
}

func (r Request) Validate() error {
	if r.ChatID <= 0 || r.LinkID < 0 || r.Watermark <= 0 || r.ServiceID < 0 {
		return ErrInvalidRequest
	}
	return nil
}

func (r Request) MarshalBSON() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	body, err := bson.Marshal(bson.D{
		{Key: "chatId", Value: r.ChatID},
		{Key: "li", Value: r.LinkID},
		{Key: "watermark", Value: r.Watermark},
		{Key: "notiRead", Value: r.NotiRead},
		{Key: "serviceId", Value: r.ServiceID},
	})
	if err != nil {
		return nil, fmt.Errorf("notiread: encode request: %w", err)
	}
	return body, nil
}

// Transport is the one-shot shared carriage request boundary. Implementations
// must not retry a failed NOTIREAD because delivery is ambiguous after a
// transport error.
type Transport interface {
	Request(context.Context, string, []byte) ([]byte, error)
}

// Send dispatches one NOTIREAD request and returns the unexamined response.
// Response status and completion-side effects are intentionally left to a
// later parity dossier; this function never retries implicitly.
func Send(ctx context.Context, transport Transport, request Request) ([]byte, error) {
	if ctx == nil || transport == nil {
		return nil, ErrInvalidRequest
	}
	body, err := request.MarshalBSON()
	if err != nil {
		return nil, err
	}
	response, err := transport.Request(ctx, Command, body)
	if err != nil {
		return nil, fmt.Errorf("notiread: transport: %w", err)
	}
	return response, nil
}
