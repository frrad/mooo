package client

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestParseChatPageEOF(t *testing.T) {
	chat, err := bson.Marshal(bson.D{{Key: "chatId", Value: int64(7)}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := bson.Marshal(bson.D{
		{Key: "chatDatas", Value: bson.A{bson.Raw(chat)}},
		{Key: "eof", Value: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	chats, eof, token, chatID, err := parseChatPage(page)
	if err != nil || !eof || token != 0 || chatID != 0 || len(chats) != 1 {
		t.Fatalf("parseChatPage = (%d, %t, %d, %d, %v)", len(chats), eof, token, chatID, err)
	}
}

func TestSessionBackgroundReaderDispatchesIdlePushAndResponse(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	s := &Session{
		wire: &wireConn{c: clientConn}, nextID: 1,
		pushes: make(chan loco.Packet, 4), pending: make(map[uint32]chan requestResult),
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
	case push := <-s.Pushes():
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
		if _, err := io.CopyN(io.Discard, serverConn, int64(parsed.BodyLen)); err != nil {
			serverDone <- err
			return
		}
		body, _ := bson.Marshal(bson.D{{Key: "status", Value: int32(0)}})
		reply, err := (loco.Packet{Header: loco.Header{PacketID: parsed.PacketID, Method: parsed.Method, BodyType: loco.BodyTypeBSON}, Body: body}).MarshalBinary(0)
		if err == nil {
			_, err = serverConn.Write(reply)
		}
		serverDone <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := s.Request(ctx, "PING", []byte{5, 0, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	_ = serverConn.Close()
}

func TestParseChatPageCursor(t *testing.T) {
	page, err := bson.Marshal(bson.D{
		{Key: "chatDatas", Value: bson.A{}},
		{Key: "eof", Value: false},
		{Key: "lastTokenId", Value: int64(11)},
		{Key: "lastChatId", Value: int64(13)},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, eof, token, chatID, err := parseChatPage(page)
	if err != nil || eof || token != 11 || chatID != 13 {
		t.Fatalf("parseChatPage = (_, %t, %d, %d, %v)", eof, token, chatID, err)
	}
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
	})
	if err != nil {
		t.Fatal(err)
	}
	var cursor loginCursor
	if err := updateLoginCursor(page, &cursor, true); err != nil {
		t.Fatal(err)
	}
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

func TestResponseStatusUsesBSONStatus(t *testing.T) {
	body, err := bson.Marshal(bson.D{{Key: "status", Value: int32(-328)}})
	if err != nil {
		t.Fatal(err)
	}
	status, err := responseStatus(loco.Packet{Header: loco.Header{Status: 0}, Body: body})
	if err != nil || status != -328 {
		t.Fatalf("responseStatus = (%d, %v), want (-328, nil)", status, err)
	}
}

func TestBookingTargets(t *testing.T) {
	body, err := bson.Marshal(bson.D{
		{Key: "ticket", Value: bson.D{{Key: "lsl", Value: bson.A{"a.example", "b.example"}}}},
		{Key: "wifi", Value: bson.D{{Key: "ports", Value: bson.A{int32(995), int32(-1)}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	hosts, ports, err := bookingTargets(body)
	if err != nil || len(hosts) != 2 || len(ports) != 1 || ports[0] != 995 {
		t.Fatalf("hosts=%v ports=%v err=%v", hosts, ports, err)
	}
}

func TestEndpointWidths(t *testing.T) {
	for _, port := range []any{int32(995), int64(995)} {
		body, err := bson.Marshal(bson.D{{Key: "host", Value: "c.example"}, {Key: "port", Value: port}})
		if err != nil {
			t.Fatal(err)
		}
		host, gotPort, err := endpoint(body)
		if err != nil || host != "c.example" || gotPort != 995 {
			t.Fatalf("host=%q port=%d err=%v", host, gotPort, err)
		}
	}
}

func TestSessionClosed(t *testing.T) {
	s := &Session{closed: true}
	_, err := s.Request(t.Context(), "CREATE", []byte{5, 0, 0, 0, 0})
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("error=%v", err)
	}
}
