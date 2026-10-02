package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestSessionMarkReadUsesOneMessageSyncShape(t *testing.T) {
	backend := newScriptedBackend(t, false, expectRequest("SYNCMSG", func(raw bson.Raw) error {
		if err := requireExactKeys(raw, "chatId", "cur", "max", "cnt"); err != nil {
			return err
		}
		if err := requireInt64(raw, "chatId", 42); err != nil {
			return err
		}
		if err := requireInt64(raw, "cur", 98); err != nil {
			return err
		}
		if err := requireInt64(raw, "max", 99); err != nil {
			return err
		}
		count, err := raw.LookupErr("cnt")
		if err != nil || count.Type != bson.TypeInt32 || count.Int32() != 1 {
			return errors.New("cnt is not int32(1)")
		}
		return nil
	}, statusDocument(bson.E{Key: "chatLogs", Value: bson.A{}})))
	session := &Session{
		wire:    backend.client,
		nextID:  1,
		pushes:  make(chan loco.Packet, 1),
		pending: make(map[uint32]chan requestResult),
	}
	go session.readLoop()

	response, err := session.MarkRead(context.Background(), 42, 99)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.ChatLogs) != 0 {
		t.Fatalf("response chat logs = %d, want empty", len(response.ChatLogs))
	}
	backend.wait(t)
}

func TestSessionMarkReadRejectsInvalidInput(t *testing.T) {
	session := &Session{}
	for _, test := range []struct {
		name      string
		ctx       context.Context
		chatID    int64
		watermark int64
		want      error
	}{
		{name: "chat id", ctx: context.Background(), chatID: 0, watermark: 1, want: syncmsg.ErrInvalidRequest},
		{name: "watermark zero", ctx: context.Background(), chatID: 42, watermark: 0, want: syncmsg.ErrInvalidRequest},
		{name: "watermark negative", ctx: context.Background(), chatID: 42, watermark: -1, want: syncmsg.ErrInvalidRequest},
		{name: "nil context", ctx: nil, chatID: 42, watermark: 1, want: ErrProtocol},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := session.MarkRead(test.ctx, test.chatID, test.watermark); !errors.Is(err, test.want) {
				t.Fatalf("MarkRead error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestClientMarkReadCommitsOnlyAfterSuccess(t *testing.T) {
	checkpoint := testCheckpoint(t)
	backend := newScriptedBackend(t, false, expectRequest("SYNCMSG", nil, statusDocument(
		bson.E{Key: "chatLogs", Value: bson.A{}},
	)))
	api := testContinuityClient(t, checkpoint, backend)
	response, err := api.MarkRead(context.Background(), 42, 99)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.ChatLogs) != 0 || checkpoint.ReadWatermark(42) != 99 {
		t.Fatalf("response=%#v watermark=%d", response, checkpoint.ReadWatermark(42))
	}
	backend.wait(t)
}

func TestClientMarkReadSkipsPersistedWatermark(t *testing.T) {
	checkpoint := testCheckpoint(t)
	if _, err := checkpoint.CommitReadWatermark(42, 99); err != nil {
		t.Fatal(err)
	}
	api, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	api.checkpoint = checkpoint
	dials := 0
	api.dial = func(context.Context, authstate.State) (*Session, error) {
		dials++
		return nil, errors.New("unexpected dial")
	}
	if _, err := api.MarkRead(context.Background(), 42, 99); err != nil {
		t.Fatal(err)
	}
	if dials != 0 {
		t.Fatalf("dials = %d, want zero for persisted watermark", dials)
	}
}

func TestClientMarkReadSkipsOlderPersistedWatermark(t *testing.T) {
	checkpoint := testCheckpoint(t)
	if _, err := checkpoint.CommitReadWatermark(42, 99); err != nil {
		t.Fatal(err)
	}
	api, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	api.checkpoint = checkpoint
	dials := 0
	api.dial = func(context.Context, authstate.State) (*Session, error) {
		dials++
		return nil, errors.New("unexpected dial")
	}
	if _, err := api.MarkRead(context.Background(), 42, 98); err != nil {
		t.Fatal(err)
	}
	if dials != 0 || checkpoint.ReadWatermark(42) != 99 {
		t.Fatalf("dials=%d watermark=%d, want no request and preserved watermark 99", dials, checkpoint.ReadWatermark(42))
	}
}

func TestClientMarkReadSkipsWatermarkAfterCheckpointReopen(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "checkpoint")
	first, err := continuity.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.CommitReadWatermark(42, 99); err != nil {
		t.Fatal(err)
	}
	second, err := continuity.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	api, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	api.checkpoint = second
	api.dial = func(context.Context, authstate.State) (*Session, error) {
		return nil, errors.New("unexpected dial after restart")
	}
	if _, err := api.MarkRead(context.Background(), 42, 99); err != nil {
		t.Fatal(err)
	}
}

func TestClientMarkReadNonzeroStatusDoesNotPersistOrRetry(t *testing.T) {
	checkpoint := testCheckpoint(t)
	requests := 0
	backend := newScriptedBackend(t, false, expectRequest("SYNCMSG", func(bson.Raw) error {
		requests++
		return nil
	}, bson.D{{Key: "status", Value: int32(-321)}}))
	api := testContinuityClient(t, checkpoint, backend)
	var status StatusError
	if _, err := api.MarkRead(context.Background(), 42, 99); !errors.As(err, &status) {
		t.Fatalf("MarkRead error = %v, want status error", err)
	}
	if requests != 1 || checkpoint.ReadWatermark(42) != 0 {
		t.Fatalf("requests=%d watermark=%d", requests, checkpoint.ReadWatermark(42))
	}
	backend.wait(t)
}

func TestClientMarkReadDisconnectDoesNotPersistOrRetry(t *testing.T) {
	checkpoint := testCheckpoint(t)
	requests := 0
	backend := newScriptedBackend(t, false, disconnectAfterRequest("SYNCMSG", &requests))
	api := testContinuityClient(t, checkpoint, backend)
	if _, err := api.MarkRead(context.Background(), 42, 99); err == nil {
		t.Fatal("MarkRead unexpectedly succeeded after disconnect")
	}
	if requests != 1 || checkpoint.ReadWatermark(42) != 0 {
		t.Fatalf("requests=%d watermark=%d", requests, checkpoint.ReadWatermark(42))
	}
	backend.wait(t)
}

func TestClientSyncMessagesRecordsObservedServerReadSideEffect(t *testing.T) {
	checkpoint := testCheckpoint(t)
	backend := newScriptedBackend(t, false, expectRequest("SYNCMSG", nil, statusDocument(
		bson.E{Key: "chatLogs", Value: bson.A{}},
	)))
	api := testContinuityClient(t, checkpoint, backend)
	_, err := api.SyncMessages(context.Background(), syncmsg.Request{
		ChatID: 42, Cur: 98, Max: 99, Count: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := checkpoint.ReadWatermark(42); got != 99 {
		t.Fatalf("SYNCMSG read watermark = %d, want 99", got)
	}
	backend.wait(t)
}

func TestClientSyncMessagesFailureDoesNotRecordReadSideEffect(t *testing.T) {
	checkpoint := testCheckpoint(t)
	backend := newScriptedBackend(t, false, expectRequest("SYNCMSG", nil,
		bson.D{{Key: "status", Value: int32(-321)}},
	))
	api := testContinuityClient(t, checkpoint, backend)
	_, err := api.SyncMessages(context.Background(), syncmsg.Request{
		ChatID: 42, Cur: 98, Max: 99, Count: 1,
	})
	if err == nil {
		t.Fatal("SYNCMSG unexpectedly succeeded")
	}
	if got := checkpoint.ReadWatermark(42); got != 0 {
		t.Fatalf("failed SYNCMSG read watermark = %d, want zero", got)
	}
	backend.wait(t)
}

func TestClientSyncMessagesDoesNotRegressReadWatermark(t *testing.T) {
	checkpoint := testCheckpoint(t)
	if _, err := checkpoint.CommitReadWatermark(42, 100); err != nil {
		t.Fatal(err)
	}
	backend := newScriptedBackend(t, false, expectRequest("SYNCMSG", nil, statusDocument(
		bson.E{Key: "chatLogs", Value: bson.A{}},
	)))
	api := testContinuityClient(t, checkpoint, backend)
	_, err := api.SyncMessages(context.Background(), syncmsg.Request{ChatID: 42, Cur: 98, Max: 99, Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := checkpoint.ReadWatermark(42); got != 100 {
		t.Fatalf("read watermark = %d, want existing higher value 100", got)
	}
	backend.wait(t)
}
