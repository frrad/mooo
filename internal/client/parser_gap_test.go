package client

import (
	"errors"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
	"testing"
)

func TestMalformedPhotoCannotBeSilentlyCommittedPast(t *testing.T) {
	checkpoint := testCheckpoint(t)
	api, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	api.checkpoint = checkpoint
	raw := make(chan loco.Packet, 2)
	out := make(chan events.Result, 2)
	for _, log := range []bson.D{
		{{Key: "logId", Value: int64(100)}, {Key: "type", Value: int32(2)}, {Key: "attachment", Value: "{}"}},
		{{Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "synthetic"}},
	} {
		body, e := bson.Marshal(bson.D{{Key: "chatId", Value: int64(42)}, {Key: "chatLog", Value: log}})
		if e != nil {
			t.Fatal(e)
		}
		raw <- loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body}
	}
	close(raw)
	decodeEventStreamWithContinuity(raw, out, checkpoint, api.queueCommit)
	first := <-out
	second := <-out
	if first.Err != nil || second.Err != nil {
		t.Fatalf("unexpected decode results: %v/%v", first.Err, second.Err)
	}
	if _, ok := first.Event.(events.MessageGap); !ok {
		t.Fatalf("first event=%T", first.Event)
	}
	if err = api.CommitEvent(second.Event); !errors.Is(err, ErrCommitOrder) {
		t.Fatalf("later message commit=%v; malformed photo was silently skipped", err)
	}
	if err = api.CommitEvent(first.Event); err != nil {
		t.Fatal(err)
	}
	if err = api.CommitEvent(second.Event); err != nil {
		t.Fatal(err)
	}
}

func TestMalformedNonMessagePacketDoesNotStopDelivery(t *testing.T) {
	raw := make(chan loco.Packet, 2)
	out := make(chan events.Result, 2)
	malformed := []byte{1, 2, 3}
	valid, err := bson.Marshal(bson.D{
		{Key: "chatId", Value: int64(42)},
		{Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "after malformed notice"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw <- loco.Packet{Header: loco.Header{Method: "CHANGESVR"}, Body: malformed}
	raw <- loco.Packet{Header: loco.Header{Method: "MSG"}, Body: valid}
	close(raw)
	terminal := false
	decodeEventStreamWithTerminal(raw, out, nil, nil, func() { terminal = true })
	first, ok := <-out
	if !ok || !errors.Is(first.Err, events.ErrMalformedEvent) {
		t.Fatalf("first result = %#v, want malformed non-message result", first)
	}
	second, ok := <-out
	if !ok || second.Err != nil {
		t.Fatalf("second result = %#v, want delivered message", second)
	}
	if _, ok := second.Event.(events.TextMessage); !ok {
		t.Fatalf("second event = %T, want TextMessage", second.Event)
	}
	if terminal {
		t.Fatal("malformed non-message packet triggered terminal interruption")
	}
}

func TestLiveMessageWithConflictingIdentityStopsAdmission(t *testing.T) {
	body, err := bson.Marshal(bson.D{
		{Key: "chatId", Value: int64(42)},
		{Key: "chatId", Value: int64(43)},
		{Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(100)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "valid"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := make(chan loco.Packet, 1)
	out := make(chan events.Result, 1)
	raw <- loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body}
	close(raw)
	interrupted := false
	decodeEventStreamWithTerminal(raw, out, nil, nil, func() { interrupted = true })
	result := <-out
	if !errors.Is(result.Err, events.ErrUnidentifiableMessage) || result.Event != nil {
		t.Fatalf("result = %#v, want identity failure", result)
	}
	if !interrupted {
		t.Fatal("conflicting message identity did not interrupt live admission")
	}
}
