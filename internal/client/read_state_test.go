package client

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/syncmsg"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// These tests run one request path on a bare session over a testloco
// backend instead of connectSession: scripting booking, checkin, and
// LOGINLIST for each would exercise nothing new. login_sync_test.go and
// client_test.go cover the bootstrap end to end.

// heldNone is a HeldMessages that holds nothing, so MarkRead falls back to
// the acknowledged position itself with cnt=0.
func heldNone(context.Context, int64, int64, int) (int64, int, error) { return 0, 0, nil }

// heldFixed returns a HeldMessages that reports logs as the caller's stored
// log IDs and records each query's (after, upTo, limit).
func heldFixed(logs []int64, queries *[][3]int64) HeldMessages {
	return func(_ context.Context, after, upTo int64, limit int) (int64, int, error) {
		*queries = append(*queries, [3]int64{after, upTo, int64(limit)})
		var last int64
		count := 0
		for _, log := range logs {
			if log > after && log <= upTo && count < limit {
				last = log
				count++
			}
		}
		return last, count, nil
	}
}

func syncShapeStep(chatID, cur, max int64, count int32) testloco.Step {
	return testloco.Expect("SYNCMSG", func(raw bson.Raw) error {
		if err := requireExactKeys(raw, "chatId", "cur", "max", "cnt"); err != nil {
			return err
		}
		if err := requireInt64(raw, "chatId", chatID); err != nil {
			return err
		}
		if err := requireInt64(raw, "cur", cur); err != nil {
			return err
		}
		if err := requireInt64(raw, "max", max); err != nil {
			return err
		}
		value, err := raw.LookupErr("cnt")
		if err != nil || value.Type != bson.TypeInt32 || value.Int32() != count {
			return fmt.Errorf("cnt is not int32(%d)", count)
		}
		return nil
	}, statusDocument(bson.E{Key: "chatLogs", Value: bson.A{}}, bson.E{Key: "isOK", Value: true}))
}

func TestClientMarkReadSendsNativeHeldShapeFromAcknowledgedPosition(t *testing.T) {
	checkpoint := testCheckpoint(t)
	if _, err := checkpoint.CommitReadWatermark(42, 90); err != nil {
		t.Fatal(err)
	}
	backend := newScriptedBackend(t, false, syncShapeStep(42, 90, 99, 3))
	api := testContinuityClient(t, checkpoint, backend)
	var queries [][3]int64
	if _, err := api.MarkRead(context.Background(), 42, 99, heldFixed([]int64{88, 90, 93, 95, 99, 104}, &queries)); err != nil {
		t.Fatal(err)
	}
	waitBackends(t, backend)
	if want := [][3]int64{{90, 99, 300}}; fmt.Sprint(queries) != fmt.Sprint(want) {
		t.Fatalf("held queries = %v, want %v", queries, want)
	}
	if got := checkpoint.ReadWatermark(42); got != 99 {
		t.Fatalf("watermark = %d, want 99", got)
	}
}

func TestClientMarkReadContinuesWhileHeldCountIsCapped(t *testing.T) {
	checkpoint := testCheckpoint(t)
	if _, err := checkpoint.CommitReadWatermark(42, 1000); err != nil {
		t.Fatal(err)
	}
	logs := make([]int64, 0, 302)
	for log := int64(1001); log <= 1302; log++ {
		logs = append(logs, log)
	}
	backend := newScriptedBackend(t, false,
		syncShapeStep(42, 1000, 1300, 300),
		syncShapeStep(42, 1300, 1302, 2),
	)
	api := testContinuityClient(t, checkpoint, backend)
	var queries [][3]int64
	if _, err := api.MarkRead(context.Background(), 42, 1302, heldFixed(logs, &queries)); err != nil {
		t.Fatal(err)
	}
	waitBackends(t, backend)
	if want := [][3]int64{{1000, 1302, 300}, {1300, 1302, 300}}; fmt.Sprint(queries) != fmt.Sprint(want) {
		t.Fatalf("held queries = %v, want %v", queries, want)
	}
	if got := checkpoint.ReadWatermark(42); got != 1302 {
		t.Fatalf("watermark = %d, want 1302", got)
	}
}

func TestClientMarkReadNotOKRaisesToReturnedLogAndContinues(t *testing.T) {
	checkpoint := testCheckpoint(t)
	if _, err := checkpoint.CommitReadWatermark(42, 90); err != nil {
		t.Fatal(err)
	}
	first := testloco.Expect("SYNCMSG", nil, statusDocument(
		bson.E{Key: "chatLogs", Value: bson.A{bson.D{{Key: "logId", Value: int64(94)}}}},
		bson.E{Key: "isOK", Value: false},
	))
	backend := newScriptedBackend(t, false, first, syncShapeStep(42, 94, 99, 2))
	api := testContinuityClient(t, checkpoint, backend)
	var queries [][3]int64
	if _, err := api.MarkRead(context.Background(), 42, 99, heldFixed([]int64{93, 95, 99}, &queries)); err != nil {
		t.Fatal(err)
	}
	waitBackends(t, backend)
	if want := [][3]int64{{90, 99, 300}, {94, 99, 300}}; fmt.Sprint(queries) != fmt.Sprint(want) {
		t.Fatalf("held queries = %v, want %v", queries, want)
	}
	if got := checkpoint.ReadWatermark(42); got != 99 {
		t.Fatalf("watermark = %d, want 99", got)
	}
}

func TestClientMarkReadNotOKWithoutLogsKeepsPosition(t *testing.T) {
	checkpoint := testCheckpoint(t)
	if _, err := checkpoint.CommitReadWatermark(42, 90); err != nil {
		t.Fatal(err)
	}
	backend := newScriptedBackend(t, false, testloco.Expect("SYNCMSG", nil, statusDocument(bson.E{Key: "isOK", Value: false})))
	api := testContinuityClient(t, checkpoint, backend)
	var queries [][3]int64
	if _, err := api.MarkRead(context.Background(), 42, 99, heldFixed([]int64{93, 99}, &queries)); err != nil {
		t.Fatal(err)
	}
	waitBackends(t, backend)
	if got := checkpoint.ReadWatermark(42); got != 90 {
		t.Fatalf("watermark = %d, want unchanged 90", got)
	}
}

func TestClientMarkReadWithoutHeldMessagesAcknowledgesPositionWithZeroCount(t *testing.T) {
	checkpoint := testCheckpoint(t)
	if _, err := checkpoint.CommitReadWatermark(42, 90); err != nil {
		t.Fatal(err)
	}
	backend := newScriptedBackend(t, false, syncShapeStep(42, 90, 99, 0))
	api := testContinuityClient(t, checkpoint, backend)
	if _, err := api.MarkRead(context.Background(), 42, 99, heldNone); err != nil {
		t.Fatal(err)
	}
	waitBackends(t, backend)
	if got := checkpoint.ReadWatermark(42); got != 99 {
		t.Fatalf("watermark = %d, want 99", got)
	}
}

func TestClientMarkReadHeldLookupFailureSendsNothing(t *testing.T) {
	checkpoint := testCheckpoint(t)
	api, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	api.checkpoint = checkpoint
	api.dial = func(context.Context, authstate.State) (*session, error) {
		return nil, errors.New("unexpected dial")
	}
	lookup := errors.New("store unavailable")
	failing := func(context.Context, int64, int64, int) (int64, int, error) { return 0, 0, lookup }
	if _, err := api.MarkRead(context.Background(), 42, 99, failing); !errors.Is(err, lookup) {
		t.Fatalf("MarkRead error = %v, want the lookup failure", err)
	}
	if got := checkpoint.ReadWatermark(42); got != 0 {
		t.Fatalf("watermark = %d, want none", got)
	}
}

func TestClientMarkReadRejectsInvalidInput(t *testing.T) {
	api, err := newClient(reusableTestState(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		ctx       context.Context
		chatID    int64
		watermark int64
		held      HeldMessages
		want      error
	}{
		{name: "chat id", ctx: context.Background(), chatID: 0, watermark: 1, held: heldNone, want: syncmsg.ErrInvalidRequest},
		{name: "watermark zero", ctx: context.Background(), chatID: 42, watermark: 0, held: heldNone, want: syncmsg.ErrInvalidRequest},
		{name: "watermark negative", ctx: context.Background(), chatID: 42, watermark: -1, held: heldNone, want: syncmsg.ErrInvalidRequest},
		{name: "nil held", ctx: context.Background(), chatID: 42, watermark: 1, held: nil, want: ErrProtocol},
		{name: "nil context", ctx: nil, chatID: 42, watermark: 1, held: heldNone, want: ErrProtocol},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := api.MarkRead(test.ctx, test.chatID, test.watermark, test.held); !errors.Is(err, test.want) {
				t.Fatalf("MarkRead error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestClientMarkReadCommitsOnlyAfterSuccess(t *testing.T) {
	checkpoint := testCheckpoint(t)
	backend := newScriptedBackend(t, false, testloco.Expect("SYNCMSG", nil, statusDocument(
		bson.E{Key: "chatLogs", Value: bson.A{}}, bson.E{Key: "isOK", Value: true},
	)))
	api := testContinuityClient(t, checkpoint, backend)
	response, err := api.MarkRead(context.Background(), 42, 99, heldNone)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.ChatLogs) != 0 || checkpoint.ReadWatermark(42) != 99 {
		t.Fatalf("response=%#v watermark=%d", response, checkpoint.ReadWatermark(42))
	}
	waitBackends(t, backend)
}

func TestClientMarkReadSerializesDuplicateWatermarks(t *testing.T) {
	checkpoint := testCheckpoint(t)
	requests := 0
	firstRequest := make(chan struct{})
	allowFirstResponse := make(chan struct{})
	backend := newScriptedBackend(t, false, func(server *testloco.Peer) error {
		request, err := server.Read()
		if err != nil {
			return err
		}
		if request.Header.Method != "SYNCMSG" {
			return fmt.Errorf("method = %q, want SYNCMSG", request.Header.Method)
		}
		requests++
		close(firstRequest)
		<-allowFirstResponse
		return server.WriteBSON(request.Header.PacketID, "SYNCMSG", statusDocument(bson.E{Key: "chatLogs", Value: bson.A{}}, bson.E{Key: "isOK", Value: true}))
	})
	api := testContinuityClient(t, checkpoint, backend)
	errs := make(chan error, 2)
	go func() {
		_, err := api.MarkRead(context.Background(), 42, 99, heldNone)
		errs <- err
	}()
	select {
	case <-firstRequest:
	case <-time.After(time.Second):
		t.Fatal("first MarkRead did not reach the transport")
	}
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		_, err := api.MarkRead(context.Background(), 42, 99, heldNone)
		errs <- err
	}()
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("second MarkRead did not start while first was blocked")
	}
	close(allowFirstResponse)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if requests != 1 || checkpoint.ReadWatermark(42) != 99 {
		t.Fatalf("requests=%d watermark=%d, want one request and watermark 99", requests, checkpoint.ReadWatermark(42))
	}
	waitBackends(t, backend)
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
	api.dial = func(context.Context, authstate.State) (*session, error) {
		dials++
		return nil, errors.New("unexpected dial")
	}
	if _, err := api.MarkRead(context.Background(), 42, 99, heldNone); err != nil {
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
	api.dial = func(context.Context, authstate.State) (*session, error) {
		dials++
		return nil, errors.New("unexpected dial")
	}
	if _, err := api.MarkRead(context.Background(), 42, 98, heldNone); err != nil {
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
	api.dial = func(context.Context, authstate.State) (*session, error) {
		return nil, errors.New("unexpected dial after restart")
	}
	if _, err := api.MarkRead(context.Background(), 42, 99, heldNone); err != nil {
		t.Fatal(err)
	}
}

func TestClientMarkReadNonzeroStatusDoesNotPersistOrRetry(t *testing.T) {
	checkpoint := testCheckpoint(t)
	requests := 0
	backend := newScriptedBackend(t, false, testloco.Expect("SYNCMSG", func(bson.Raw) error {
		requests++
		return nil
	}, bson.D{{Key: "status", Value: int32(-321)}}))
	api := testContinuityClient(t, checkpoint, backend)
	var status StatusError
	if _, err := api.MarkRead(context.Background(), 42, 99, heldNone); !errors.As(err, &status) {
		t.Fatalf("MarkRead error = %v, want status error", err)
	}
	if requests != 1 || checkpoint.ReadWatermark(42) != 0 {
		t.Fatalf("requests=%d watermark=%d", requests, checkpoint.ReadWatermark(42))
	}
	waitBackends(t, backend)
}

func TestClientMarkReadDisconnectDoesNotPersistOrRetry(t *testing.T) {
	checkpoint := testCheckpoint(t)
	requests := 0
	backend := newScriptedBackend(t, false, testloco.DropAfter("SYNCMSG", &requests))
	api := testContinuityClient(t, checkpoint, backend)
	if _, err := api.MarkRead(context.Background(), 42, 99, heldNone); err == nil {
		t.Fatal("MarkRead unexpectedly succeeded after disconnect")
	}
	if requests != 1 || checkpoint.ReadWatermark(42) != 0 {
		t.Fatalf("requests=%d watermark=%d", requests, checkpoint.ReadWatermark(42))
	}
	waitBackends(t, backend)
}

func TestClientSyncMessagesCatchUpDoesNotSuppressLaterMarkRead(t *testing.T) {
	checkpoint := testCheckpoint(t)
	var counts []int32
	recordCount := func(raw bson.Raw) error {
		count, err := raw.LookupErr("cnt")
		if err != nil || count.Type != bson.TypeInt32 {
			return errors.New("cnt is not int32")
		}
		counts = append(counts, count.Int32())
		return nil
	}
	backend := newScriptedBackend(t, false,
		testloco.Expect("SYNCMSG", recordCount, statusDocument(bson.E{Key: "chatLogs", Value: bson.A{}})),
		testloco.Expect("SYNCMSG", recordCount, statusDocument(bson.E{Key: "chatLogs", Value: bson.A{}}, bson.E{Key: "isOK", Value: true})),
	)
	api := testContinuityClient(t, checkpoint, backend)
	// Catch-up declares zero held messages. Whether that marks the interval
	// read on the server is unproven, so it must not record an acknowledgement.
	if _, err := api.syncMessages(context.Background(), syncmsg.Request{ChatID: 42, Cur: 90, Max: 99, Count: 0}); err != nil {
		t.Fatal(err)
	}
	if got := checkpoint.ReadWatermark(42); got != 0 {
		t.Fatalf("catch-up recorded read acknowledgement %d, want none", got)
	}
	var queries [][3]int64
	if _, err := api.MarkRead(context.Background(), 42, 99, heldFixed([]int64{99}, &queries)); err != nil {
		t.Fatal(err)
	}
	if len(counts) != 2 || counts[0] != 0 || counts[1] != 1 {
		t.Fatalf("SYNCMSG cnt sequence = %v, want [0 1]", counts)
	}
	if got := checkpoint.ReadWatermark(42); got != 99 {
		t.Fatalf("read acknowledgement after MarkRead = %d, want 99", got)
	}
	waitBackends(t, backend)
}
