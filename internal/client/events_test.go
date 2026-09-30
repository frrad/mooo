package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDecodeEventStreamContinuesAfterMalformedPacket(t *testing.T) {
	raw := make(chan loco.Packet, 2)
	output := make(chan events.Result, 2)
	malformed, _ := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}})
	unknown, _ := bson.Marshal(bson.D{{Key: "status", Value: int32(0)}})
	raw <- loco.Packet{Header: loco.Header{Method: "MSG"}, Body: malformed}
	raw <- loco.Packet{Header: loco.Header{Method: "KICKOUT"}, Body: unknown}
	close(raw)
	decodeEventStream(raw, output)

	first := <-output
	if !errors.Is(first.Err, events.ErrMalformedEvent) || first.Event != nil {
		t.Fatalf("first result = %#v", first)
	}
	second := <-output
	kickout, ok := second.Event.(events.Kickout)
	if second.Err != nil || !ok || kickout.Reason != 0 {
		t.Fatalf("second result = %#v", second)
	}
	if _, ok := <-output; ok {
		t.Fatal("event output did not close")
	}
}

func TestDecodeEventStreamSuppressesCommittedAndInProcessDuplicates(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := continuity.Open(filepath.Join(dir, "checkpoint"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkpoint.CommitMessage(42, 9); err != nil {
		t.Fatal(err)
	}
	raw := make(chan loco.Packet, 4)
	output := make(chan events.Result, 4)
	message := func(logID int64, text string) loco.Packet {
		body, err := bson.Marshal(bson.D{
			{Key: "chatId", Value: int64(42)},
			{Key: "chatLog", Value: bson.D{{Key: "logId", Value: logID}, {Key: "type", Value: int32(1)}, {Key: "message", Value: text}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body}
	}
	raw <- message(9, "already committed")
	raw <- message(10, "new")
	raw <- message(10, "duplicate")
	close(raw)
	decodeEventStreamWithContinuity(raw, output, checkpoint, nil)

	result, ok := <-output
	if !ok || result.Err != nil {
		t.Fatalf("result = %#v", result)
	}
	messageEvent, ok := result.Event.(events.TextMessage)
	if !ok || messageEvent.LogID != 10 || messageEvent.Message != "new" {
		t.Fatalf("event = %#v", result.Event)
	}
	if _, ok := <-output; ok {
		t.Fatal("duplicate event was emitted")
	}
}

func TestRawPushConsumerPreventsTypedConsumer(t *testing.T) {
	state := reusableTestState()
	api, err := newClient(state, nil)
	if err != nil {
		t.Fatal(err)
	}
	pushes := make(chan loco.Packet)
	api.dial = func(context.Context, authstate.State) (*Session, error) {
		return &Session{pushes: pushes}, nil
	}
	if _, err := api.Pushes(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Events(t.Context()); !errors.Is(err, ErrPushConsumerSelected) {
		t.Fatalf("Events after Pushes error = %v", err)
	}
}
