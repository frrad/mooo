package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
)

// GETMSGS reads chosen messages by position: parallel chatIds and logIds,
// answered with the current chat logs (for example an edited text).
func TestGetMessagesReadsEditedMessagesByLogID(t *testing.T) {
	backend := newScriptedBackend(t, true, func(server *wireConn) error {
		request, err := server.readRequest()
		if err != nil {
			return err
		}
		var body struct {
			ChatIDs []int64 `bson:"chatIds"`
			LogIDs  []int64 `bson:"logIds"`
		}
		if request.Header.Method != "GETMSGS" || bson.Unmarshal(request.Body, &body) != nil ||
			len(body.ChatIDs) != 1 || body.ChatIDs[0] != 42 || len(body.LogIDs) != 1 || body.LogIDs[0] != 101 {
			return errors.New("unexpected GETMSGS request")
		}
		return writeBackendPacket(server, request.Header.PacketID, "GETMSGS", mustBSON(statusDocument(bson.E{Key: "chatLogs", Value: bson.A{
			bson.D{{Key: "chatId", Value: int64(42)}, {Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(1)}, {Key: "authorId", Value: int64(2000)}, {Key: "revision", Value: int32(2)}, {Key: "message", Value: "synthetic second edit"}},
		}})))
	})
	session := &Session{
		wire: backend.client, nextID: 1, pushes: make(chan loco.Packet, 4),
		pending: make(map[uint32]chan requestResult), userID: 7, appVersion: "26.8.0",
	}
	go session.readLoop()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got, err := session.GetMessages(ctx, 42, []int64{101})
	if err != nil {
		t.Fatal(err)
	}
	backend.wait(t)
	if len(got) != 1 {
		t.Fatalf("got %d events", len(got))
	}
	text, ok := got[0].(events.TextMessage)
	if !ok || text.LogID != 101 || text.Revision != 2 || text.Message != "synthetic second edit" {
		t.Fatalf("event = %#v", got[0])
	}
}

func TestGetMessagesRejectsALogFromAnotherChat(t *testing.T) {
	backend := newScriptedBackend(t, true, func(server *wireConn) error {
		request, err := server.readRequest()
		if err != nil {
			return err
		}
		return writeBackendPacket(server, request.Header.PacketID, "GETMSGS", mustBSON(statusDocument(bson.E{Key: "chatLogs", Value: bson.A{
			bson.D{{Key: "chatId", Value: int64(43)}, {Key: "logId", Value: int64(101)}, {Key: "type", Value: int32(1)}, {Key: "message", Value: "synthetic"}},
		}})))
	})
	session := &Session{
		wire: backend.client, nextID: 1, pushes: make(chan loco.Packet, 4),
		pending: make(map[uint32]chan requestResult), userID: 7, appVersion: "26.8.0",
	}
	go session.readLoop()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := session.GetMessages(ctx, 42, []int64{101}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("err = %v", err)
	}
	backend.wait(t)
}
