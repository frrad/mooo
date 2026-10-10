package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
	"github.com/frrad/mooo/internal/protocol/sessionlogin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// White-box tests: they drive a bare session or wireConn over a raw net.Pipe
// so they can control framing, write blocking, pending-request state, or
// read-loop timing directly. A scripted testloco peer cannot express those
// conditions, so these tests do not go through connectSession.

func TestSessionBackgroundReaderDispatchesIdlePushAndResponse(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	s := &session{
		wire: &wireConn{c: clientConn}, nextID: 1,
		pushes: make(chan loco.Packet, 4), pending: make(map[uint32]pendingRequest),
	}
	go s.readLoop()
	defer func() { _ = s.Close() }()

	pushBody, _ := bson.Marshal(bson.D{{Key: "chatId", Value: int64(7)}})
	pushRaw, err := (loco.Packet{Header: loco.Header{Method: "MSG", BodyType: loco.BodyTypeBSON}, Body: pushBody}).MarshalBinary(0)
	if err != nil {
		t.Fatal(err)
	}
	writeErr := make(chan error, 1)
	go func() {
		_, err := serverConn.Write(pushRaw)
		writeErr <- err
	}()
	select {
	case push := <-s.pushStream():
		if push.Header.Method != "MSG" {
			t.Fatalf("push method = %q", push.Header.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("idle push was not delivered")
	}
	if err := <-writeErr; err != nil {
		t.Fatal(err)
	}

	serverDone := make(chan error, 1)
	go func() {
		header := make([]byte, loco.HeaderSize)
		if _, err := io.ReadFull(serverConn, header); err != nil {
			serverDone <- err
			return
		}
		parsed, err := loco.ParseHeader(header, 0)
		if err != nil {
			serverDone <- err
			return
		}
		body := make([]byte, parsed.BodyLen)
		if _, err := io.ReadFull(serverConn, body); err != nil {
			serverDone <- err
			return
		}
		if !bytes.Equal(body, []byte{5, 0, 0, 0, 0}) {
			serverDone <- fmt.Errorf("PING body = %v, want empty BSON document", body)
			return
		}
		replyBody, _ := bson.Marshal(bson.D{{Key: "status", Value: int32(0)}})
		reply, err := (loco.Packet{Header: loco.Header{PacketID: parsed.PacketID, Method: parsed.Method, BodyType: loco.BodyTypeBSON}, Body: replyBody}).MarshalBinary(0)
		if err == nil {
			_, err = serverConn.Write(reply)
		}
		serverDone <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := s.request(ctx, "PING", []byte{5, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	_ = serverConn.Close()
}

func TestUpdateLoginCursorCapturesInventoryAndDeletion(t *testing.T) {
	page, err := bson.Marshal(bson.D{
		{Key: "chatDatas", Value: bson.A{
			bson.D{
				{Key: "c", Value: int64(42)},
				{Key: "l", Value: bson.D{{Key: "chatId", Value: int64(42)}, {Key: "logId", Value: int64(105)}}},
			},
			bson.D{{Key: "c", Value: int64(7)}, {Key: "l", Value: nil}},
		}},
		{Key: "delChatIds", Value: bson.A{int64(9)}},
		{Key: "lastTokenId", Value: int64(12)},
		{Key: "status", Value: int32(0)},
		{Key: "eof", Value: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := sessionlogin.DecodeLoginListPage(page)
	if err != nil {
		t.Fatal(err)
	}
	var cursor loginCursor
	cursor.apply(decoded)
	if cursor.lastTokenID == nil || *cursor.lastTokenID != 12 {
		t.Fatalf("last token = %v", cursor.lastTokenID)
	}
	if len(cursor.observed) != 2 || cursor.observed[0].ChatID != 42 || cursor.observed[0].MaxLogID != 105 || cursor.observed[1].ChatID != 7 || cursor.observed[1].MaxLogID != 0 {
		t.Fatalf("observed inventory = %#v", cursor.observed)
	}
	if len(cursor.deleted) != 1 || cursor.deleted[0] != 9 {
		t.Fatalf("deleted inventory = %v", cursor.deleted)
	}
}

func TestSessionClosed(t *testing.T) {
	s := &session{closed: true}
	_, err := s.request(t.Context(), "CREATE", []byte{5, 0, 0, 0, 0})
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("error=%v", err)
	}
}
