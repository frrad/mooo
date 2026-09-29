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

func TestSendTextValidatesBeforeTransport(t *testing.T) {
	var session *Session
	_, err := session.SendText(context.Background(), 0, "hello")
	if !errors.Is(err, chat.ErrInvalidChatID) {
		t.Fatalf("SendText error = %v, want ErrInvalidChatID", err)
	}
}

func TestSendReplyValidatesBeforeTransport(t *testing.T) {
	var session *Session
	_, err := session.SendReply(context.Background(), chat.ReplyRequest{})
	if !errors.Is(err, chat.ErrInvalidMessage) {
		t.Fatalf("SendReply error = %v, want ErrInvalidMessage", err)
	}
}

func TestSendImageValidatesBeforeTransport(t *testing.T) {
	var session *Session
	_, err := session.SendImage(context.Background(), 1, []byte("not an image"))
	if err == nil {
		t.Fatal("SendImage unexpectedly accepted invalid image")
	}
}
