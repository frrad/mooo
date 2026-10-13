package client

import (
	"errors"
	"testing"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func acceptAll(events.Event) bool { return true }

func TestSeedLastMessageAdmitsNeverDeliveredChatAndQueuesItsCommit(t *testing.T) {
	checkpoint := testCheckpoint(t)
	api, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	api.checkpoint = checkpoint
	chat := chatmeta.ChatData{
		ChatID: 42,
		Type:   "DirectChat",
		LastChatLog: mustBSON(bson.D{
			{Key: "logId", Value: int64(500)},
			{Key: "chatId", Value: int64(42)},
			{Key: "authorId", Value: int64(7)},
			{Key: "type", Value: int32(1)},
			{Key: "message", Value: "latest"},
		}),
	}

	seeded, err := api.SeedLastMessage(chat, acceptAll)
	if err != nil {
		t.Fatal(err)
	}

	text, ok := seeded.(events.TextMessage)
	if !ok || text.ChatID != 42 || text.LogID != 500 || text.AuthorID != 7 || text.Message != "latest" {
		t.Fatalf("seeded = %#v, want text 42/500 from 7", seeded)
	}
	if got := checkpoint.Snapshot().CommittedMax(42); got != 0 {
		t.Fatalf("committed before delivery = %d, want 0", got)
	}
	if err := api.CommitEvent(seeded); err != nil {
		t.Fatalf("commit seeded message: %v", err)
	}
	if got := checkpoint.Snapshot().CommittedMax(42); got != 500 {
		t.Fatalf("committed after delivery = %d, want 500", got)
	}
}

func TestSeedLastMessageSkipsDeliveredAndRejectedChats(t *testing.T) {
	lastLog := mustBSON(bson.D{
		{Key: "logId", Value: int64(500)},
		{Key: "authorId", Value: int64(7)},
		{Key: "type", Value: int32(1)},
		{Key: "message", Value: "latest"},
	})
	cases := []struct {
		name    string
		chat    chatmeta.ChatData
		prepare func(t *testing.T, api *Client)
		accept  func(events.Event) bool
	}{
		{
			name: "already committed",
			chat: chatmeta.ChatData{ChatID: 42, LastChatLog: lastLog},
			prepare: func(t *testing.T, api *Client) {
				if _, err := api.checkpoint.CommitMessage(42, 400); err != nil {
					t.Fatal(err)
				}
			},
			accept: acceptAll,
		},
		{
			name: "live delivery already admitted",
			chat: chatmeta.ChatData{ChatID: 42, LastChatLog: lastLog},
			prepare: func(t *testing.T, api *Client) {
				if err := api.checkpoint.RecordDeliveryStart(42, 501); err != nil {
					t.Fatal(err)
				}
			},
			accept: acceptAll,
		},
		{
			name:    "no last message",
			chat:    chatmeta.ChatData{ChatID: 42},
			prepare: func(*testing.T, *Client) {},
			accept:  acceptAll,
		},
		{
			name:    "rejected by the caller",
			chat:    chatmeta.ChatData{ChatID: 42, LastChatLog: lastLog},
			prepare: func(*testing.T, *Client) {},
			accept:  func(events.Event) bool { return false },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api, err := newClient(reusableTestState(), nil)
			if err != nil {
				t.Fatal(err)
			}
			api.checkpoint = testCheckpoint(t)
			tc.prepare(t, api)

			seeded, err := api.SeedLastMessage(tc.chat, tc.accept)

			if err != nil || seeded != nil {
				t.Fatalf("SeedLastMessage = %#v, %v; want nil, nil", seeded, err)
			}
			if pending := api.pendingCommits[42]; len(pending) != 0 {
				t.Fatalf("pending commits = %v, want none", pending)
			}
		})
	}
}

func TestSeedLastMessageRejectsMisdirectedLastLog(t *testing.T) {
	api, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	api.checkpoint = testCheckpoint(t)
	chat := chatmeta.ChatData{
		ChatID: 42,
		LastChatLog: mustBSON(bson.D{
			{Key: "logId", Value: int64(500)},
			{Key: "chatId", Value: int64(43)},
			{Key: "authorId", Value: int64(7)},
			{Key: "type", Value: int32(1)},
			{Key: "message", Value: "foreign"},
		}),
	}

	seeded, err := api.SeedLastMessage(chat, acceptAll)

	if !errors.Is(err, events.ErrChatLogMisdirected) || seeded != nil {
		t.Fatalf("SeedLastMessage = %#v, %v; want misdirected error", seeded, err)
	}
	if pending := api.pendingCommits[42]; len(pending) != 0 {
		t.Fatalf("pending commits = %v, want none", pending)
	}
}
