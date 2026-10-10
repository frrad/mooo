package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/media"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	testloco "github.com/frrad/mooo/internal/testsupport/loco"
)

// These tests run one request path on a bare session over a testloco
// backend instead of connectSession: scripting booking, checkin, and
// LOGINLIST for each would exercise nothing new. login_sync_test.go and
// client_test.go cover the bootstrap end to end.

// A file goes through SHIP, POST and COMPLETE once, with its type, extension
// and name; the bytes arrive unchanged.
func TestSendUploadSendsFileTypeNameAndBytesOnce(t *testing.T) {
	fileData := []byte("synthetic outbound file")
	upload, err := media.PrepareUpload("synthetic.txt", fileData, "")
	if err != nil {
		t.Fatal(err)
	}
	ships := 0
	mainBackend := newScriptedBackend(t, true, func(server *testloco.Peer) error {
		request, err := server.Read()
		if err != nil {
			return err
		}
		ships++
		body := bson.Raw(request.Body)
		if request.Header.Method != "SHIP" || body.Lookup("t").Int32() != messagetype.File || body.Lookup("e").StringValue() != "txt" ||
			body.Lookup("s").Int64() != int64(len(fileData)) || body.Lookup("cs").StringValue() != upload.Checksum {
			return errors.New("unexpected SHIP")
		}
		return server.WriteBSON(request.Header.PacketID, "SHIP", statusDocument(
			bson.E{Key: "k", Value: "synthetic-ticket"}, bson.E{Key: "vh", Value: "media.invalid"}, bson.E{Key: "p", Value: int32(995)},
		))
	})
	completeLog := mustBSON(bson.D{{Key: "logId", Value: int64(104)}, {Key: "type", Value: int32(18)}, {Key: "sendAt", Value: int64(1)}})
	mediaBackend := newScriptedBackend(t, true, func(server *testloco.Peer) error {
		request, err := server.Read()
		if err != nil {
			return err
		}
		body := bson.Raw(request.Body)
		if request.Header.Method != "POST" || body.Lookup("t").Int32() != messagetype.File || body.Lookup("f").StringValue() != "synthetic.txt" ||
			body.Lookup("k").StringValue() != "synthetic-ticket" || body.Lookup("c").Int64() != 42 || body.Lookup("u").Int64() != 7 {
			return errors.New("unexpected POST")
		}
		if err := server.WriteBSON(request.Header.PacketID, "POST", statusDocument()); err != nil {
			return err
		}
		payload, err := server.ReadPayload()
		if err != nil {
			return err
		}
		if !bytes.Equal(payload, fileData) {
			return errors.New("uploaded file bytes mismatch")
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
	result, err := session.SendUpload(ctx, 42, upload)
	if err != nil || result.ChatLog.Lookup("logId").Int64() != 104 {
		t.Fatalf("file upload err=%v", err)
	}
	waitBackends(t, mediaBackend)
	waitBackends(t, mainBackend)
	if ships != 1 {
		t.Fatalf("SHIP sent %d times", ships)
	}
}

func TestSendUploadRejectsUnpreparedUploadBeforeTransport(t *testing.T) {
	session := &session{}
	if _, err := session.SendUpload(context.Background(), 42, media.Upload{}); !errors.Is(err, media.ErrInvalidUpload) {
		t.Fatalf("err = %v", err)
	}
}

// A missing media COMPLETE is ambiguous: the upload must fail without retrying
// SHIP, POST, or the byte stream, and it must not tear down the main LOCO
// session, which may still deliver the resulting chat log as a push. The main
// backend is held open until the first attempt has been checked, then the test
// waits for the reader to observe its disconnect before asserting ErrClosed,
// so the result does not depend on how the reader and writer are scheduled.
func TestScriptedBackendAmbiguousPhotoCompleteIsNeverRetried(t *testing.T) {
	imageData := syntheticClientJPEG(t)
	shipRequests := 0
	releaseMain := make(chan struct{})
	mainBackend := newScriptedBackend(t, true, func(server *testloco.Peer) error {
		request, err := server.Read()
		if err != nil {
			return err
		}
		if request.Header.Method != "SHIP" {
			return fmt.Errorf("method = %q, want SHIP", request.Header.Method)
		}
		shipRequests++
		if err := server.WriteBSON(request.Header.PacketID, "SHIP", statusDocument(
			bson.E{Key: "k", Value: "synthetic-ticket"}, bson.E{Key: "vh", Value: "media.invalid"}, bson.E{Key: "p", Value: int32(995)},
		)); err != nil {
			return err
		}
		<-releaseMain
		return nil
	})
	postRequests := 0
	mediaBackend := newScriptedBackend(t, true, func(server *testloco.Peer) error {
		request, err := server.Read()
		if err != nil {
			return err
		}
		if request.Header.Method != "POST" {
			return fmt.Errorf("method = %q, want POST", request.Header.Method)
		}
		postRequests++
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
		return server.Conn.Close()
	})
	mediaDials := 0
	media := hostDialer(testloco.Hosts{"media.invalid": mediaBackend})
	session := &session{
		wire: clientWire(mainBackend), nextID: 1, pushes: make(chan loco.Packet, 4),
		pending: make(map[uint32]pendingRequest), userID: 7, appVersion: "26.8.0",
		mediaDial: func(ctx context.Context, host string, port int) (*wireConn, error) {
			mediaDials++
			return media(ctx, host, port)
		},
	}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		session.readLoop()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := session.SendImage(ctx, 42, imageData, "")
	if err == nil {
		t.Fatal("photo without COMPLETE unexpectedly succeeded")
	}
	if !errors.Is(err, io.EOF) || errors.Is(err, ErrClosed) {
		t.Fatalf("first photo error = %v, want media COMPLETE EOF", err)
	}
	waitBackends(t, mediaBackend)
	session.mu.Lock()
	closedByUpload := session.closed
	session.mu.Unlock()
	if closedByUpload {
		t.Fatal("ambiguous media COMPLETE closed the main session")
	}

	close(releaseMain)
	waitBackends(t, mainBackend)
	select {
	case <-readerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not observe main disconnect")
	}
	if _, err := session.SendImage(ctx, 42, imageData, ""); !errors.Is(err, ErrClosed) {
		t.Fatalf("second photo error = %v, want ErrClosed", err)
	}
	if shipRequests != 1 || postRequests != 1 || mediaDials != 1 {
		t.Fatalf("SHIP=%d POST=%d media dials=%d, want one each", shipRequests, postRequests, mediaDials)
	}
}
