package client

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
)

type bodyProgressOwnerSpy struct {
	toggles []byte
	closed  bool
}

func (s *bodyProgressOwnerSpy) Toggle(enable byte) (bool, error) {
	s.toggles = append(s.toggles, enable)
	return true, nil
}
func (s *bodyProgressOwnerSpy) Close() { s.closed = true }

type chunkConn struct {
	*bytes.Reader
	chunks []int
	index  int
}

func (c *chunkConn) Read(p []byte) (int, error) {
	if c.index < len(c.chunks) {
		n := c.chunks[c.index]
		c.index++
		if n < len(p) {
			p = p[:n]
		}
	}
	return c.Reader.Read(p)
}
func (*chunkConn) Write([]byte) (int, error)        { return 0, io.ErrClosedPipe }
func (*chunkConn) Close() error                     { return nil }
func (*chunkConn) LocalAddr() net.Addr              { return testAddr("local") }
func (*chunkConn) RemoteAddr() net.Addr             { return testAddr("remote") }
func (*chunkConn) SetDeadline(time.Time) error      { return nil }
func (*chunkConn) SetReadDeadline(time.Time) error  { return nil }
func (*chunkConn) SetWriteDeadline(time.Time) error { return nil }

type testAddr string

func (a testAddr) Network() string { return string(a) }
func (a testAddr) String() string  { return string(a) }

func TestSessionBodyProgressBindingExcludesHeaderAndResetsPartialBody(t *testing.T) {
	owner := &bodyProgressOwnerSpy{}
	session := newSession(nil)
	if err := session.BindInSegmentTimeout(owner); err != nil {
		t.Fatal(err)
	}
	header, err := (loco.Header{PacketID: 7, Method: "PUSH", BodyLen: 4}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	input := append(header, []byte("body")...)
	wire := &wireConn{c: &chunkConn{Reader: bytes.NewReader(input), chunks: []int{loco.HeaderSize, 1, 3}}}
	var observed bool
	packet, err := wire.readWithHeaderObserverAndProgress(func(loco.Header) { observed = true }, session.bodyProgressCallbacks())
	if err != nil {
		t.Fatal(err)
	}
	if !observed || string(packet.Body) != "body" {
		t.Fatalf("observed=%v packet=%q", observed, packet.Body)
	}
	want := []byte{1, 0, 1, 0}
	if !bytes.Equal(owner.toggles, want) {
		t.Fatalf("toggles=%v, want %v", owner.toggles, want)
	}
}

func TestSessionBodyProgressBindingDoesNotArmOnBlockedHeader(t *testing.T) {
	owner := &bodyProgressOwnerSpy{}
	session := newSession(nil)
	if err := session.BindInSegmentTimeout(owner); err != nil {
		t.Fatal(err)
	}
	header, err := (loco.Header{PacketID: 7, Method: "PUSH", BodyLen: 4}).MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	wire := &wireConn{c: &chunkConn{Reader: bytes.NewReader(header[:loco.HeaderSize-1])}}
	_, err = wire.readWithHeaderObserverAndProgress(nil, session.bodyProgressCallbacks())
	if err == nil {
		t.Fatal("short header unexpectedly succeeded")
	}
	if len(owner.toggles) != 0 {
		t.Fatalf("header toggles=%v, want none", owner.toggles)
	}
}
