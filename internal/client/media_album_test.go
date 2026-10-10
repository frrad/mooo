package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/media"
)

func albumItemBackend(t *testing.T, key string, want []byte) *scriptedBackend {
	t.Helper()
	return newScriptedBackend(t, true, func(server *wireConn) error {
		request, err := server.readRequest()
		if err != nil {
			return err
		}
		body := bson.Raw(request.Body)
		if request.Header.Method != "MPOST" || body.Lookup("k").StringValue() != key || body.Lookup("t").Int32() != media.MultiPhotoType {
			return fmt.Errorf("unexpected item request %s", request.Header.Method)
		}
		if err := writeBackendPacket(server, request.Header.PacketID, "MPOST", mustBSON(statusDocument())); err != nil {
			return err
		}
		payload, err := readSecurePayload(server)
		if err != nil {
			return err
		}
		if !bytes.Equal(payload, want) {
			return errors.New("album photo bytes mismatch")
		}
		return writeBackendPacket(server, 0, "COMPLETE", mustBSON(statusDocument()))
	})
}

// An album reserves every photo with one MSHIP, uploads each photo once on its
// own media connection in order, and only then creates the message with one
// type-27 WRITE listing the uploaded photos.
func TestSendAlbumUploadsEachPhotoThenWritesOnce(t *testing.T) {
	first, second := syntheticClientJPEG(t), syntheticClientPNG(t)
	var writes []bson.Raw
	mainBackend := newScriptedBackend(t, true, func(server *wireConn) error {
		request, err := server.readRequest()
		if err != nil {
			return err
		}
		if request.Header.Method != "MSHIP" || bson.Raw(request.Body).Lookup("t").Int32() != media.MultiPhotoType {
			return fmt.Errorf("unexpected %s", request.Header.Method)
		}
		if err := writeBackendPacket(server, request.Header.PacketID, "MSHIP", mustBSON(statusDocument(
			bson.E{Key: "kl", Value: bson.A{"k1", "k2"}}, bson.E{Key: "mtl", Value: bson.A{"image/jpeg", "image/png"}},
			bson.E{Key: "vhl", Value: bson.A{"media1.invalid", "media2.invalid"}}, bson.E{Key: "pl", Value: bson.A{int32(995), int32(996)}},
		))); err != nil {
			return err
		}
		request, err = server.readRequest()
		if err != nil {
			return err
		}
		if request.Header.Method != "WRITE" {
			return fmt.Errorf("unexpected %s", request.Header.Method)
		}
		writes = append(writes, bson.Raw(request.Body))
		return writeBackendPacket(server, request.Header.PacketID, "WRITE", mustBSON(statusDocument(
			bson.E{Key: "chatId", Value: int64(42)}, bson.E{Key: "logId", Value: int64(105)}, bson.E{Key: "sendAt", Value: int64(1)},
		)))
	})
	items := map[string]*scriptedBackend{
		"media1.invalid": albumItemBackend(t, "k1", first),
		"media2.invalid": albumItemBackend(t, "k2", second),
	}
	var dialed []string
	session := &Session{
		wire: mainBackend.client, nextID: 1, pushes: make(chan loco.Packet, 4),
		pending: make(map[uint32]chan requestResult), userID: 7, appVersion: "26.8.0",
		mediaDial: func(_ context.Context, host string, _ int) (*wireConn, error) {
			dialed = append(dialed, host)
			return items[host].client, nil
		},
	}
	go session.readLoop()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	response, err := session.SendAlbum(ctx, 42, [][]byte{first, second}, "synthetic album caption")
	if err != nil || response.LogID != 105 {
		t.Fatalf("album err=%v response=%+v", err, response)
	}
	for _, backend := range items {
		backend.wait(t)
	}
	mainBackend.wait(t)
	if len(dialed) != 2 || dialed[0] != "media1.invalid" || dialed[1] != "media2.invalid" {
		t.Fatalf("dialed %q", dialed)
	}
	if len(writes) != 1 || writes[0].Lookup("type").Int32() != media.MultiPhotoType || writes[0].Lookup("msg").StringValue() != "" {
		t.Fatalf("writes = %v", writes)
	}
	var extra struct {
		Keys     []string `json:"kl"`
		Comments []string `json:"cmtl"`
	}
	if err := json.Unmarshal([]byte(writes[0].Lookup("extra").StringValue()), &extra); err != nil || len(extra.Keys) != 2 || extra.Comments[0] != "synthetic album caption" {
		t.Fatalf("extra = %+v err=%v", extra, err)
	}
}

// A photo upload failure stops the album before WRITE, so no message exists
// and the failure is reported as not sent.
func TestSendAlbumUploadFailureNeverWrites(t *testing.T) {
	first, second := syntheticClientJPEG(t), syntheticClientPNG(t)
	mainBackend := newScriptedBackend(t, true, func(server *wireConn) error {
		request, err := server.readRequest()
		if err != nil {
			return err
		}
		if err := writeBackendPacket(server, request.Header.PacketID, "MSHIP", mustBSON(statusDocument(
			bson.E{Key: "kl", Value: bson.A{"k1", "k2"}}, bson.E{Key: "mtl", Value: bson.A{"image/jpeg", "image/png"}},
			bson.E{Key: "vhl", Value: bson.A{"media1.invalid", "media2.invalid"}}, bson.E{Key: "pl", Value: bson.A{int32(995), int32(996)}},
		))); err != nil {
			return err
		}
		// Any further request (a WRITE) would fail the test through wait.
		if _, err := server.readRequest(); err == nil {
			return errors.New("album was written after a failed upload")
		}
		return nil
	})
	failing := newScriptedBackend(t, true, func(server *wireConn) error {
		request, err := server.readRequest()
		if err != nil {
			return err
		}
		return writeBackendPacket(server, request.Header.PacketID, "MPOST", mustBSON(bson.D{{Key: "status", Value: int32(-1)}}))
	})
	session := &Session{
		wire: mainBackend.client, nextID: 1, pushes: make(chan loco.Packet, 4),
		pending: make(map[uint32]chan requestResult), userID: 7, appVersion: "26.8.0",
		mediaDial: func(context.Context, string, int) (*wireConn, error) { return failing.client, nil },
	}
	go session.readLoop()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := session.SendAlbum(ctx, 42, [][]byte{first, second}, "")
	if !errors.Is(err, ErrAlbumNotCreated) {
		t.Fatalf("err = %v, want ErrAlbumNotCreated", err)
	}
	failing.wait(t)
	_ = mainBackend.client.close()
	mainBackend.wait(t)
}

func syntheticClientPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Set(0, 1, color.RGBA{B: 0xff, A: 0xff})
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
