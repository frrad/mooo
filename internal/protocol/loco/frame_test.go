package loco

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestHeaderPinnedSyntheticVector(t *testing.T) {
	header := Header{PacketID: 0x11223344, Status: 0x5566, Method: "TEST", BodyType: 0x77, BodyLen: 0x8899aabb}
	got, err := header.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x44, 0x33, 0x22, 0x11, 0x66, 0x55, 'T', 'E', 'S', 'T', 0, 0, 0, 0, 0, 0, 0, 0x77, 0xbb, 0xaa, 0x99, 0x88}
	if !bytes.Equal(got, want) {
		t.Fatalf("header mismatch: %x", got)
	}
}

func TestParseHeaderRejectsNonzeroPadding(t *testing.T) {
	header, _ := (Header{PacketID: 1, Method: "A"}).MarshalBinary()
	header[8] = 'X'
	if _, err := ParseHeader(header, 64); !errors.Is(err, ErrInvalidMethod) {
		t.Fatalf("got %v", err)
	}
}

func TestPacketMarshalDerivesBodyLength(t *testing.T) {
	wire, err := (Packet{Header: Header{PacketID: 7, Method: "PING", BodyLen: 99}, Body: []byte{1, 2, 3}}).MarshalBinary(8)
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(wire[18:22]); got != 3 {
		t.Fatalf("body length = %d", got)
	}
}
