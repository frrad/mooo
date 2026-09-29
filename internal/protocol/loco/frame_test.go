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

func TestParserFragmentedAndCoalesced(t *testing.T) {
	one, _ := (Packet{Header: Header{PacketID: 1, Method: "ONE"}, Body: []byte("abc")}).MarshalBinary(64)
	two, _ := (Packet{Header: Header{PacketID: 2, Method: "TWO"}, Body: []byte("defg")}).MarshalBinary(64)
	stream := append(one, two...)
	p := NewParser(64)
	var packets []Packet
	for _, part := range [][]byte{stream[:3], stream[3:25], stream[25:]} {
		got, err := p.Feed(part)
		if err != nil {
			t.Fatal(err)
		}
		packets = append(packets, got...)
	}
	if len(packets) != 2 || packets[0].Header.Method != "ONE" || string(packets[1].Body) != "defg" {
		t.Fatalf("unexpected packets: %#v", packets)
	}
}

func TestParserFailsClosedOnHostileLength(t *testing.T) {
	header, _ := (Header{PacketID: 1, Method: "TEST", BodyLen: 65}).MarshalBinary()
	p := NewParser(64)
	if _, err := p.Feed(header); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("got %v", err)
	}
	if _, err := p.Feed(nil); !errors.Is(err, ErrParserFailed) {
		t.Fatalf("failed parser resumed: %v", err)
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
