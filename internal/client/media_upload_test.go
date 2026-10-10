package client

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/media"
)

// A file goes through SHIP, POST and COMPLETE once, with its type, extension
// and name; the bytes arrive unchanged.
func TestSendUploadSendsFileTypeNameAndBytesOnce(t *testing.T) {
	fileData := []byte("synthetic outbound file")
	upload, err := media.PrepareUpload("synthetic.txt", fileData, "")
	if err != nil {
		t.Fatal(err)
	}
	ships := 0
	mainBackend := newScriptedBackend(t, true, func(server *wireConn) error {
		request, err := server.readRequest()
		if err != nil {
			return err
		}
		ships++
		body := bson.Raw(request.Body)
		if request.Header.Method != "SHIP" || body.Lookup("t").Int32() != media.FileType || body.Lookup("e").StringValue() != "txt" ||
			body.Lookup("s").Int64() != int64(len(fileData)) || body.Lookup("cs").StringValue() != upload.Checksum {
			return errors.New("unexpected SHIP")
		}
		return writeBackendPacket(server, request.Header.PacketID, "SHIP", mustBSON(statusDocument(
			bson.E{Key: "k", Value: "synthetic-ticket"}, bson.E{Key: "vh", Value: "media.invalid"}, bson.E{Key: "p", Value: int32(995)},
		)))
	})
	completeLog := mustBSON(bson.D{{Key: "logId", Value: int64(104)}, {Key: "type", Value: int32(18)}, {Key: "sendAt", Value: int64(1)}})
	mediaBackend := newScriptedBackend(t, true, func(server *wireConn) error {
		request, err := server.readRequest()
		if err != nil {
			return err
		}
		body := bson.Raw(request.Body)
		if request.Header.Method != "POST" || body.Lookup("t").Int32() != media.FileType || body.Lookup("f").StringValue() != "synthetic.txt" ||
			body.Lookup("k").StringValue() != "synthetic-ticket" || body.Lookup("c").Int64() != 42 || body.Lookup("u").Int64() != 7 {
			return errors.New("unexpected POST")
		}
		if err := writeBackendPacket(server, request.Header.PacketID, "POST", mustBSON(statusDocument())); err != nil {
			return err
		}
		payload, err := readSecurePayload(server)
		if err != nil {
			return err
		}
		if !bytes.Equal(payload, fileData) {
			return errors.New("uploaded file bytes mismatch")
		}
		return writeBackendPacket(server, 0, "COMPLETE", mustBSON(statusDocument(bson.E{Key: "chatLog", Value: bson.Raw(completeLog)})))
	})
	session := &Session{
		wire: mainBackend.client, nextID: 1, pushes: make(chan loco.Packet, 4),
		pending: make(map[uint32]chan requestResult), userID: 7, appVersion: "26.8.0",
		mediaDial: func(context.Context, string, int) (*wireConn, error) { return mediaBackend.client, nil },
	}
	go session.readLoop()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := session.SendUpload(ctx, 42, upload)
	if err != nil || result.ChatLog.Lookup("logId").Int64() != 104 {
		t.Fatalf("file upload err=%v", err)
	}
	mediaBackend.wait(t)
	mainBackend.wait(t)
	if ships != 1 {
		t.Fatalf("SHIP sent %d times", ships)
	}
}

func TestSendUploadRejectsUnpreparedUploadBeforeTransport(t *testing.T) {
	session := &Session{}
	if _, err := session.SendUpload(context.Background(), 42, media.Upload{}); !errors.Is(err, media.ErrInvalidUpload) {
		t.Fatalf("err = %v", err)
	}
}
