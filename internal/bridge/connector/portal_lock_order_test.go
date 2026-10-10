package connector

import (
	"context"
	"sync"
	"testing"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/events"
)

// Observed in owned acceptance: the SDK runs portal events synchronously and
// holds the portal's event lock while it calls a Matrix handler. The Kakao
// pump held the connector gate while queueing into that same portal, and the
// Matrix handler waited for the gate, so both stopped for good. portalLock
// stands in for the SDK's per-portal event lock on both sides.
func TestMatrixMessageDoesNotDeadlockWithInboundPortalDelivery(t *testing.T) {
	fake := &fakeKakao{sendResp: chat.WriteResponse{ChatID: testChatID, LogID: 31, SendAt: 1700000002}}
	kc := connectedClient(t, fake)
	var portalLock sync.Mutex
	pumpQueued := make(chan struct{})
	kc.queue = func(bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		close(pumpQueued)
		portalLock.Lock()
		defer portalLock.Unlock()
		return bridgev2.EventHandlingResult{Success: true}
	}

	portalLock.Lock()
	pumpDone := make(chan bool)
	go func() {
		pumpDone <- kc.handleEvent(fake, events.TextMessage{ChatID: testChatID, LogID: 12, AuthorID: testOtherID, Message: "inbound"})
	}()
	<-pumpQueued
	matrixDone := make(chan error)
	go func() {
		_, err := kc.HandleMatrixMessage(context.Background(), matrixMessage(event.MsgText, "outbound"))
		portalLock.Unlock()
		matrixDone <- err
	}()

	select {
	case err := <-matrixDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Matrix handler and Kakao pump deadlocked on the portal lock and connector gate")
	}
	if !<-pumpDone {
		t.Fatal("inbound delivery was not handled after the Matrix send")
	}
	if len(fake.sends) != 1 {
		t.Fatalf("sent %d times", len(fake.sends))
	}
}
