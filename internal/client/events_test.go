package client

import (
	"context"
	"errors"
	"testing"

	"github.com/frrad/mooo/internal/authstate"
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
	packet, ok := second.Event.(events.UnknownPacket)
	if second.Err != nil || !ok || packet.Method != "KICKOUT" {
		t.Fatalf("second result = %#v", second)
	}
	if _, ok := <-output; ok {
		t.Fatal("event output did not close")
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
