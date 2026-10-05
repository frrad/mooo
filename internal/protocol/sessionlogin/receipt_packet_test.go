package sessionlogin

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"

	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
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
			assertFramedReceiptBody(t, wire[loco.HeaderSize:], tc.body)
		})
	}
}

func assertFramedReceiptBody(t *testing.T, encoded []byte, input ReceiptBody) {
	t.Helper()
	raw := bson.Raw(encoded)
	elements, err := raw.Elements()
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]int32{}
	if input.Kind == ReceiptBodyBlockSync {
		expected = map[string]int32{"r": input.Revision, "pr": input.PlusRevision}
	}
	if len(elements) != len(expected) {
		t.Fatalf("body keys=%d want %d", len(elements), len(expected))
	}
	for _, element := range elements {
		want, ok := expected[element.Key()]
		if !ok {
			t.Fatalf("unexpected body key %q", element.Key())
		}
		value := element.Value()
		if value.Type != bson.TypeInt32 || len(value.Value) != 4 {
			t.Fatalf("%s type=%v width=%d want BSON int32 width 4", element.Key(), value.Type, len(value.Value))
		}
		got := int32(binary.LittleEndian.Uint32(value.Value))
		if got != want {
			t.Fatalf("%s=%d want %d", element.Key(), got, want)
		}
	}
}

func TestBuildReceiptPacketMatchesPlaintextGoldenFrame(t *testing.T) {
	wire, err := BuildReceiptPacket(ReceiptPacket{
		PacketID: 0x01020304,
		Method:   "CUSTOM",
		Body:     ReceiptBody{Kind: ReceiptBodyHint},
	}, 64)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{
		0x04, 0x03, 0x02, 0x01, // uint32 packet ID, little-endian
		0x00, 0x00, // status
		'C', 'U', 'S', 'T', 'O', 'M', 0, 0, 0, 0, 0, // 11-byte method field
		0x00,                   // BSON body type
		0x05, 0x00, 0x00, 0x00, // body length
		0x05, 0x00, 0x00, 0x00, 0x00, // canonical empty BSON document
	}
	if !bytes.Equal(wire, want) {
		t.Fatalf("wire=%x want %x", wire, want)
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
