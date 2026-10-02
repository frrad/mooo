package client

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
)

func TestWireRequestRequiresMethodAndPacketIDMatch(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	_ = clientConn.SetDeadline(time.Now().Add(time.Second))
	_ = serverConn.SetDeadline(time.Now().Add(time.Second))
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	wire := &wireConn{c: clientConn}
	body := []byte("request")
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
		requestBody := make([]byte, parsed.BodyLen)
		if _, err := io.ReadFull(serverConn, requestBody); err != nil {
			serverDone <- err
			return
		}
		wrong, err := (loco.Packet{Header: loco.Header{PacketID: parsed.PacketID, Method: "WRONG"}, Body: []byte("wrong")}).MarshalBinary(0)
		if err != nil {
			serverDone <- err
			return
		}
		right, err := (loco.Packet{Header: loco.Header{PacketID: parsed.PacketID, Method: parsed.Method}, Body: []byte("right")}).MarshalBinary(0)
		if err != nil {
			serverDone <- err
			return
		}
		if _, err := serverConn.Write(wrong); err != nil {
			serverDone <- err
			return
		}
		_, err = serverConn.Write(right)
		serverDone <- err
	}()
	got, unsolicited, err := wire.request(17, "PING", body)
	if err != nil {
		t.Fatalf("request err=%v unsolicited=%#v", err, unsolicited)
	}
	if got.Header.Method != "PING" || !bytes.Equal(got.Body, []byte("right")) {
		t.Fatalf("response=%#v", got)
	}
	if len(unsolicited) != 1 || unsolicited[0].Header.Method != "WRONG" {
		t.Fatalf("unsolicited=%#v", unsolicited)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not finish")
	}
}
