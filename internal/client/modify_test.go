package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/loco"
)

func scriptedSession(t *testing.T, script func(*wireConn) error) (*Session, *scriptedBackend) {
	t.Helper()
	backend := newScriptedBackend(t, true, script)
	session := &Session{
		wire: backend.client, nextID: 1, pushes: make(chan loco.Packet, 4),
		pending: make(map[uint32]chan requestResult), userID: 7, appVersion: "26.8.0",
	}
	go session.readLoop()
	return session, backend
}

func TestModifyMessageSendsOnceAndReturnsTheRevision(t *testing.T) {
	session, backend := scriptedSession(t, func(server *wireConn) error {
		request, err := server.readRequest()
		if err != nil {
			return err
		}
		if request.Header.Method != "MODIFYMSG" || bson.Raw(request.Body).Lookup("msg").StringValue() != "synthetic edit" {
			return errors.New("unexpected MODIFYMSG")
		}
		return writeBackendPacket(server, request.Header.PacketID, "MODIFYMSG", mustBSON(statusDocument(
			bson.E{Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(102)}, {Key: "type", Value: int32(0)}}},
			bson.E{Key: "modifiedChatLog", Value: bson.D{{Key: "logId", Value: int64(101)}, {Key: "revision", Value: int32(1)}}},
		)))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	revision, err := session.ModifyMessage(ctx, chat.ModifyRequest{ChatID: 42, LogID: 101, Type: chat.TextType, Message: "synthetic edit", Extra: "{}"})
	if err != nil || revision != 1 {
		t.Fatalf("revision=%d err=%v", revision, err)
	}
	backend.wait(t)
}

func TestDeleteMessageReportsServerRefusalAsStatus(t *testing.T) {
	session, backend := scriptedSession(t, func(server *wireConn) error {
		request, err := server.readRequest()
		if err != nil {
			return err
		}
		if request.Header.Method != "DELETEMSG" || bson.Raw(request.Body).Lookup("logId").Int64() != 101 {
			return errors.New("unexpected DELETEMSG")
		}
		return writeBackendPacket(server, request.Header.PacketID, "DELETEMSG", mustBSON(bson.D{{Key: "status", Value: int32(chat.StatusDeleteTimeExpired)}}))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := session.DeleteMessage(ctx, chat.DeleteRequest{ChatID: 42, LogID: 101})
	var status StatusError
	if !errors.As(err, &status) || status.Status != chat.StatusDeleteTimeExpired {
		t.Fatalf("err = %v", err)
	}
	backend.wait(t)
}
