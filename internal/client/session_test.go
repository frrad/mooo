package client

import (
	"errors"
	"testing"

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
