package client

import (
	"bytes"
	"net"
	"slices"
	"sync"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/loco"
)

type recordedPackets struct {
	mu      sync.Mutex
	packets []loco.Packet
}

func (r *recordedPackets) observe(packet loco.Packet) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.packets = append(r.packets, packet)
}

func (r *recordedPackets) methods() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	methods := make([]string, len(r.packets))
	for i, packet := range r.packets {
		methods[i] = packet.Header.Method
	}
	return methods
}

func TestPacketObserverSeesWireReadWithoutAlteringPacket(t *testing.T) {
	recorder := &recordedPackets{}
	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	body := []byte{0x05, 0x00, 0x00, 0x00, 0x00}
	frame, err := (loco.Packet{Header: loco.Header{PacketID: 3, Method: "MSG", BodyType: loco.BodyTypeBSON}, Body: body}).MarshalBinary(0)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = serverConn.Write(frame) }()

	packet, err := (&wireConn{c: clientConn, observer: recorder.observe}).read()
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(packet.Body, body) || packet.Header.PacketID != 3 || packet.Header.Method != "MSG" {
		t.Fatalf("observer altered the production packet: %+v", packet)
	}
	if len(recorder.packets) != 1 || recorder.packets[0].Header.PacketID != 3 || !bytes.Equal(recorder.packets[0].Body, body) {
		t.Fatalf("observed packets = %+v, want packet 3", recorder.packets)
	}
}

func TestPacketObserverSeesEveryBootstrapReplyAndLeavesLoginUnchanged(t *testing.T) {
	recorder := &recordedPackets{}
	dialers, backends := scriptedLoginAttempt(t, "token", bson.D{
		{Key: "status", Value: int32(0)},
		{Key: "chatDatas", Value: bson.A{bson.D{{Key: "c", Value: int64(42)}}}},
		{Key: "eof", Value: true},
		{Key: "lastTokenId", Value: int64(91)},
	})

	session, err := connectSession(t.Context(), reusableTestState(), sessionConfig{resume: continuity.Checkpoint{Version: continuity.Version}, dialers: dialers.observed(recorder.observe), ping: realtimePingOptions()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	if len(session.initialChatData) != 1 {
		t.Fatalf("initial chats = %d, want 1", len(session.initialChatData))
	}
	methods := recorder.methods()
	for _, want := range []string{"GETCONF", "CHECKIN", "LOGINLIST"} {
		if !slices.Contains(methods, want) {
			t.Fatalf("observed methods = %v, missing %s", methods, want)
		}
	}
	_ = session.Close()
	waitBackends(t, backends...)
}
