package connector

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/protocol/chat"
)

// Observed in owned acceptance: while the bridge was unresponsive the
// homeserver re-sent its appservice transaction, the SDK handled every copy of
// the same Matrix event, and each copy reached Kakao. One Matrix event must
// produce at most one source send, including after a restart.
func TestRedeliveredMatrixEventIsSentToKakaoOnce(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	if _, err := kc.CreateGroup(ctx, &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}); err != nil {
		t.Fatal(err)
	}
	backend.sendResp = chat.WriteResponse{ChatID: 5000, LogID: 200, SendAt: 1700000000}
	portal, err := kc.login.Bridge.GetPortalByMXID(ctx, "!selected:test")
	if err != nil {
		t.Fatal(err)
	}
	deliver := func(client *KakaoClient) error {
		_, err := client.HandleMatrixMessage(ctx, &bridgev2.MatrixMessage{MatrixEventBase: bridgev2.MatrixEventBase[*event.MessageEventContent]{
			Event:   &event.Event{ID: "$synthetic-redelivered", RoomID: "!selected:test", Sender: "@owner:test"},
			Portal:  portal,
			Content: &event.MessageEventContent{MsgType: event.MsgText, Body: "synthetic outbound once"},
		}})
		return err
	}

	if err := deliver(kc); err != nil {
		t.Fatal(err)
	}
	err = deliver(kc)
	if err == nil {
		t.Fatal("redelivered Matrix event reported a second successful send")
	}
	if status := bridgev2.WrapErrorInStatus(err); !status.DisableMSS {
		t.Fatal("duplicate delivery would overwrite the original event's status")
	}

	restarted := newKakaoClient(kc.login, testSelfID, nil)
	restarted.client = backend
	if err := deliver(restarted); err == nil {
		t.Fatal("redelivery after restart reported a second successful send")
	}
	if len(backend.sends) != 1 {
		t.Fatalf("one Matrix event produced %d Kakao sends", len(backend.sends))
	}
}

func TestOutboundWithoutMatrixEventIdentityIsNotSent(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	if _, err := kc.CreateGroup(ctx, &bridgev2.GroupCreateParams{Type: "regular", RoomID: "!selected:test", Participants: []networkid.UserID{"2000", "4000"}}); err != nil {
		t.Fatal(err)
	}
	portal, err := kc.login.Bridge.GetPortalByMXID(ctx, "!selected:test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = kc.HandleMatrixMessage(ctx, &bridgev2.MatrixMessage{MatrixEventBase: bridgev2.MatrixEventBase[*event.MessageEventContent]{
		Portal:  portal,
		Content: &event.MessageEventContent{MsgType: event.MsgText, Body: "synthetic without identity"},
	}})
	if err == nil || len(backend.sends) != 0 {
		t.Fatal("a send without a Matrix event identity could not be deduplicated")
	}
}
