package sessionlogin

import (
	"bytes"
	"errors"
	"math"
	"testing"

	"github.com/frrad/mooo/internal/protocol/loco"
)

func TestBuildReceiptPacketCopiesExplicitHeaderAndBody(t *testing.T) {
	cases := []struct {
		name     string
		packetID uint32
		method   string
		body     ReceiptBody
		bodyLen  uint32
	}{
		{name: "zero hint", packetID: 0, method: "HINT", body: ReceiptBody{Kind: ReceiptBodyHint}, bodyLen: 5},
		{name: "max block sync", packetID: math.MaxUint32, method: "BLOCKSYNC", body: ReceiptBody{Kind: ReceiptBodyBlockSync, Revision: math.MinInt32, PlusRevision: math.MaxInt32}, bodyLen: 20},
		{name: "method is copied", packetID: 17, method: "CUSTOM", body: ReceiptBody{Kind: ReceiptBodyHint}, bodyLen: 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wire, err := BuildReceiptPacket(ReceiptPacket{PacketID: tc.packetID, Method: tc.method, Body: tc.body}, 64)
			if err != nil {
				t.Fatal(err)
			}
			header, err := loco.ParseHeader(wire, 64)
			if err != nil {
				t.Fatal(err)
			}
			if header.PacketID != tc.packetID || header.Method != tc.method || header.Status != 0 || header.BodyType != loco.BodyTypeBSON || header.BodyLen != tc.bodyLen {
				t.Fatalf("header=%+v", header)
			}
			body, err := BuildReceiptBody(ReceiptBody{Kind: tc.body.Kind, PacketID: tc.packetID, Revision: tc.body.Revision, PlusRevision: tc.body.PlusRevision})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(wire[loco.HeaderSize:], body) {
				t.Fatalf("framed body=%x want %x", wire[loco.HeaderSize:], body)
			}
		})
	}
}

func TestBuildReceiptPacketRejectsFramingInputs(t *testing.T) {
	base := ReceiptPacket{PacketID: 17, Method: "HINT", Body: ReceiptBody{Kind: ReceiptBodyHint}}
	for _, tc := range []struct {
		name  string
		input ReceiptPacket
		limit uint32
		want  error
	}{
		{name: "empty method", input: ReceiptPacket{PacketID: base.PacketID, Body: base.Body}, want: loco.ErrInvalidMethod},
		{name: "method too long", input: ReceiptPacket{PacketID: base.PacketID, Method: "METHOD-LONGER", Body: base.Body}, want: loco.ErrInvalidMethod},
		{name: "method non ascii", input: ReceiptPacket{PacketID: base.PacketID, Method: "HINTé", Body: base.Body}, want: loco.ErrInvalidMethod},
		{name: "body limit", input: base, limit: 4, want: loco.ErrBodyTooLarge},
		{name: "unknown body", input: ReceiptPacket{PacketID: base.PacketID, Method: base.Method, Body: ReceiptBody{Kind: 99}}, want: ErrInvalidReceiptBodyKind},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildReceiptPacket(tc.input, tc.limit)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want %v", err, tc.want)
			}
		})
	}
}
