package client

import (
	"context"
	"errors"
	"testing"

	"github.com/frrad/mooo/internal/protocol/chat"
)

func TestCreateChatValidatesBeforeTransport(t *testing.T) {
	var session *Session
	_, err := session.CreateChat(context.Background(), chat.CreateRequest{})
	if !errors.Is(err, chat.ErrNoMembers) {
		t.Fatalf("CreateChat error = %v, want ErrNoMembers", err)
	}
}
