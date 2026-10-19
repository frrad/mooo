package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/media"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
)

// Regression for frrad/mooo#428: a video larger than one secure frame
// (loco.DefaultMaxBody) failed with "ciphertext too large" after SHIP and
// POST. The stream must arrive whole, split across several frames, each
// within the frame limit, starting at the server's resume offset.
func TestSendUploadStreamsPayloadLargerThanOneSecureFrame(t *testing.T) {
	videoData := make([]byte, loco.DefaultMaxBody+5<<20+123)
	for i := range videoData {
		videoData[i] = byte(i * 31)
	}
	upload, err := media.PrepareUpload("synthetic.mp4", videoData, "")
	if err != nil {
		t.Fatal(err)
	}
	const resumeOffset = 1000
	mainBackend := newScriptedBackend(t, true, func(server *testloco.Peer) error {
		request, err := server.Read()
		if err != nil {
			return err
		}
		if request.Header.Method != "SHIP" {
			return fmt.Errorf("method = %q, want SHIP", request.Header.Method)
		}
		return server.WriteBSON(request.Header.PacketID, "SHIP", statusDocument(
			bson.E{Key: "k", Value: "synthetic-ticket"}, bson.E{Key: "vh", Value: "media.invalid"}, bson.E{Key: "p", Value: int32(995)},
		))
	})
	frames := 0
	completeLog := mustBSON(bson.D{{Key: "logId", Value: int64(428)}, {Key: "type", Value: int32(3)}, {Key: "sendAt", Value: int64(1)}})
	mediaBackend := newScriptedBackend(t, true, func(server *testloco.Peer) error {
		request, err := server.Read()
		if err != nil {
			return err
		}
		if request.Header.Method != "POST" {
			return fmt.Errorf("method = %q, want POST", request.Header.Method)
		}
		if err := server.WriteBSON(request.Header.PacketID, "POST", statusDocument(bson.E{Key: "o", Value: int64(resumeOffset)})); err != nil {
			return err
		}
		var received []byte
		for len(received) < len(videoData)-resumeOffset {
			payload, err := server.ReadPayload()
			if err != nil {
				return fmt.Errorf("frame %d: %w", frames+1, err)
			}
			frames++
			received = append(received, payload...)
		}
		if !bytes.Equal(received, videoData[resumeOffset:]) {
			return errors.New("uploaded video bytes mismatch")
		}
		return server.WriteBSON(0, "COMPLETE", statusDocument(bson.E{Key: "chatLog", Value: bson.Raw(completeLog)}))
	})
	session := &session{
		wire: clientWire(mainBackend), nextID: 1, pushes: make(chan loco.Packet, 4),
		pending: make(map[uint32]pendingRequest), userID: 7, appVersion: "26.8.0",
		mediaDial: func(context.Context, string, int) (*wireConn, error) { return clientWire(mediaBackend), nil },
	}
	go session.readLoop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := session.SendUpload(ctx, 42, upload)
	if err != nil {
		t.Fatalf("SendUpload err = %v", err)
	}
	if got := result.ChatLog.Lookup("logId").Int64(); got != 428 {
		t.Fatalf("logId = %d, want 428", got)
	}
	waitBackends(t, mediaBackend)
	waitBackends(t, mainBackend)
	if frames < 2 {
		t.Fatalf("stream used %d secure frames, want several", frames)
	}
}
