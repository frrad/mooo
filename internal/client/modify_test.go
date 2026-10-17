package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/chat"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
)

// These tests run one request path on a bare session over a testloco
// backend instead of connectSession: scripting booking, checkin, and
// LOGINLIST for each would exercise nothing new. login_sync_test.go and
// client_test.go cover the bootstrap end to end.

func TestModifyMessageSendsOnceAndReturnsTheRevision(t *testing.T) {
	session, backend := scriptedSession(t, func(server *testloco.Peer) error {
		request, err := server.Read()
		if err != nil {
			return err
		}
		if request.Header.Method != "MODIFYMSG" || bson.Raw(request.Body).Lookup("msg").StringValue() != "synthetic edit" {
			return errors.New("unexpected MODIFYMSG")
		}
		return server.WriteBSON(request.Header.PacketID, "MODIFYMSG", statusDocument(
			bson.E{Key: "chatLog", Value: bson.D{{Key: "logId", Value: int64(102)}, {Key: "type", Value: int32(0)}}},
			bson.E{Key: "modifiedChatLog", Value: bson.D{{Key: "logId", Value: int64(101)}, {Key: "revision", Value: int32(1)}}},
		))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	revision, err := session.ModifyMessage(ctx, chat.ModifyRequest{ChatID: 42, LogID: 101, Type: messagetype.Text, Message: "synthetic edit", Extra: "{}"})
	if err != nil || revision != 1 {
		t.Fatalf("revision=%d err=%v", revision, err)
	}
	waitBackends(t, backend)
}

func TestDeleteMessageReportsServerRefusalAsStatus(t *testing.T) {
	session, backend := scriptedSession(t, func(server *testloco.Peer) error {
		request, err := server.Read()
		if err != nil {
			return err
		}
		if request.Header.Method != "DELETEMSG" || bson.Raw(request.Body).Lookup("logId").Int64() != 101 {
			return errors.New("unexpected DELETEMSG")
		}
		return server.WriteBSON(request.Header.PacketID, "DELETEMSG", bson.D{{Key: "status", Value: int32(chat.StatusDeleteTimeExpired)}})
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := session.DeleteMessage(ctx, chat.DeleteRequest{ChatID: 42, LogID: 101})
	var status StatusError
	if !errors.As(err, &status) || status.Status != chat.StatusDeleteTimeExpired {
		t.Fatalf("err = %v", err)
	}
	waitBackends(t, backend)
}

func TestEditSourceReadsTheTargetWithOneGetMessages(t *testing.T) {
	session, backend := scriptedSession(t, func(server *testloco.Peer) error {
		request, err := server.Read()
		if err != nil {
			return err
		}
		body := bson.Raw(request.Body)
		if request.Header.Method != "GETMSGS" || body.Lookup("logIds", "0").Int64() != 101 || body.Lookup("chatIds", "0").Int64() != 42 {
			return errors.New("unexpected GETMSGS")
		}
		return server.WriteBSON(request.Header.PacketID, "GETMSGS", statusDocument(bson.E{Key: "chatLogs", Value: bson.A{bson.D{
			{Key: "logId", Value: int64(101)}, {Key: "chatId", Value: int64(42)}, {Key: "authorId", Value: int64(2000)}, {Key: "type", Value: int32(12)},
			{Key: "message", Value: "synthetic caption"}, {Key: "attachment", Value: `{"path":"synthetic/emot_001.png"}`},
		}}}))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	source, err := session.EditSource(ctx, 42, 101)
	if err != nil || source.Type != messagetype.Sticker || source.AuthorID != 2000 || source.Message != "synthetic caption" || source.Attachment != `{"path":"synthetic/emot_001.png"}` {
		t.Fatalf("source = %+v, err = %v", source, err)
	}
	waitBackends(t, backend)
}
