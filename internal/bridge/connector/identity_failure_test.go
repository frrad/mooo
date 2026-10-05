package connector

import (
	"context"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestWireIdentityFailureReportsTerminalStateAndDoesNotRecover(t *testing.T) {
	body, err := bson.Marshal(bson.D{{Key: "chatId", Value: testChatID}, {Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(0)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "synthetic"}}}})
	if err != nil {
		t.Fatal(err)
	}
	_, decodeErr := events.DecodeForDelivery(loco.Packet{Header: loco.Header{Method: "MSG"}, Body: body})
	if !isUnidentifiableMessageError(decodeErr) {
		t.Fatalf("wire decode error = %v, want typed identity failure", decodeErr)
	}
	fake := &fakeKakao{stream: make(chan events.Result)}
	kc, harness := newTestClient(t, nil)
	kc.wait = func(ctx context.Context, _ time.Duration) error { <-ctx.Done(); return ctx.Err() }
	kc.client = fake
	kc.cleanup = fake
	kc.generation = 1
	t.Cleanup(kc.Disconnect)
	stream := make(chan events.Result, 1)
	stream <- events.Result{Err: decodeErr}
	close(stream)
	kc.run(fake, stream, make(chan struct{}), 1)
	state := harness.lastState()
	if state.Error != stateUnidentifiableMsg {
		t.Fatalf("wire identity state = %#v, want terminal identity code", state)
	}
	kc.mu.Lock()
	retrying := kc.retryCancel != nil
	kc.mu.Unlock()
	if retrying {
		t.Fatal("wire identity failure scheduled automatic recovery")
	}
}

func TestBootstrapIdentityFailureReportsTerminalState(t *testing.T) {
	fake := &fakeKakao{catchUps: map[int64]catchUpResult{testChatID: {err: events.ErrUnidentifiableMessage}}}
	fake.resumeTargets = []syncmsg.Target{{ChatID: testChatID, MaxLogID: 9}}
	kc, harness := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	kc.mu.Lock()
	kc.generation = 1
	kc.mu.Unlock()
	kc.connectOnce(context.Background(), 1, false)
	if state := harness.lastState(); state.Error != stateUnidentifiableMsg {
		t.Fatalf("bootstrap identity state = %#v, want terminal identity code", state)
	}
}
