package client

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/loco"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
)

// These tests run one request path on a bare session over a testloco
// backend instead of connectSession: scripting booking, checkin, and
// LOGINLIST for each would exercise nothing new. login_sync_test.go and
// client_test.go cover the bootstrap end to end.

// Cancelling the caller's context interrupts an upload that is waiting for
// the media server's COMPLETE, instead of holding the media connection until
// its I/O deadline. The outcome stays ambiguous and nothing is retried.
func TestSendImageCancellationInterruptsPendingComplete(t *testing.T) {
	imageData := syntheticClientJPEG(t)
	releaseMain := make(chan struct{})
	mainBackend := newScriptedBackend(t, true, func(server *testloco.Peer) error {
		request, err := server.Read()
		if err != nil {
			return err
		}
		if err := server.WriteBSON(request.Header.PacketID, "SHIP", statusDocument(
			bson.E{Key: "k", Value: "synthetic-ticket"}, bson.E{Key: "vh", Value: "media.invalid"}, bson.E{Key: "p", Value: int32(995)},
		)); err != nil {
			return err
		}
		<-releaseMain
		return nil
	})
	uploaded := make(chan struct{})
	mediaBackend := newScriptedBackend(t, true, func(server *testloco.Peer) error {
		request, err := server.Read()
		if err != nil {
			return err
		}
		if err := server.WriteBSON(request.Header.PacketID, "POST", statusDocument()); err != nil {
			return err
		}
		payload, err := server.ReadPayload()
		if err != nil {
			return err
		}
		if !bytes.Equal(payload, imageData) {
			return errors.New("uploaded photo bytes mismatch")
		}
		close(uploaded)
		// Withhold COMPLETE until the client gives up and closes.
		_, _ = server.Read()
		return nil
	})
	session := &session{
		wire: clientWire(mainBackend), nextID: 1, pushes: make(chan loco.Packet, 4),
		pending: make(map[uint32]pendingRequest), userID: 7, appVersion: "26.8.0",
		mediaDial: func(context.Context, string, int) (*wireConn, error) { return clientWire(mediaBackend), nil },
	}
	go session.readLoop()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-uploaded
		cancel()
	}()
	started := time.Now()
	_, err := session.SendImage(ctx, 42, imageData, "")
	if err == nil {
		t.Fatal("upload without COMPLETE unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("cancelled upload returned after %v", elapsed)
	}
	waitBackends(t, mediaBackend)
	close(releaseMain)
	waitBackends(t, mainBackend)
}

// A caption travels in the POST extra JSON as cmt; SHIP is unchanged.
func TestSendImageCarriesCaptionInPostExtra(t *testing.T) {
	imageData := syntheticClientJPEG(t)
	mainBackend := newScriptedBackend(t, true, func(server *testloco.Peer) error {
		request, err := server.Read()
		if err != nil {
			return err
		}
		if got := bson.Raw(request.Body).Lookup("ex").StringValue(); got != "{}" {
			return errors.New("SHIP extra changed")
		}
		return server.WriteBSON(request.Header.PacketID, "SHIP", statusDocument(
			bson.E{Key: "k", Value: "synthetic-ticket"}, bson.E{Key: "vh", Value: "media.invalid"}, bson.E{Key: "p", Value: int32(995)},
		))
	})
	completeLog := mustBSON(bson.D{{Key: "logId", Value: int64(103)}, {Key: "type", Value: int32(2)}})
	mediaBackend := newScriptedBackend(t, true, func(server *testloco.Peer) error {
		request, err := server.Read()
		if err != nil {
			return err
		}
		if got := bson.Raw(request.Body).Lookup("ex").StringValue(); got != `{"cmt":"synthetic caption"}` {
			return errors.New("POST extra lacks the caption")
		}
		if err := server.WriteBSON(request.Header.PacketID, "POST", statusDocument()); err != nil {
			return err
		}
		if _, err := server.ReadPayload(); err != nil {
			return err
		}
		return server.WriteBSON(0, "COMPLETE", statusDocument(bson.E{Key: "chatLog", Value: bson.Raw(completeLog)}))
	})
	session := &session{
		wire: clientWire(mainBackend), nextID: 1, pushes: make(chan loco.Packet, 4),
		pending: make(map[uint32]pendingRequest), userID: 7, appVersion: "26.8.0",
		mediaDial: func(context.Context, string, int) (*wireConn, error) { return clientWire(mediaBackend), nil },
	}
	go session.readLoop()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := session.SendImage(ctx, 42, imageData, "synthetic caption")
	if err != nil || result.ChatLog.Lookup("logId").Int64() != 103 {
		t.Fatalf("captioned photo err=%v", err)
	}
	waitBackends(t, mediaBackend)
	waitBackends(t, mainBackend)
}
