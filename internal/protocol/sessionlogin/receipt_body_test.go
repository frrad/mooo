package sessionlogin

import (
	"bytes"
	"encoding/binary"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestBuildReceiptBodyHintIsCanonicalEmptyDocument(t *testing.T) {
	for _, packetID := range []uint32{0, 17, ^uint32(0)} {
		body, err := BuildReceiptBody(ReceiptBody{Kind: ReceiptBodyHint, PacketID: packetID})
		if err != nil {
			t.Fatal(err)
		}
		if want := []byte{5, 0, 0, 0, 0}; !bytes.Equal(body, want) {
			t.Fatalf("packet ID %d body=%v want %v", packetID, body, want)
		}
	}
}

func TestBuildReceiptBodyBlockSyncUsesRenamedInt32Fields(t *testing.T) {
	body, err := BuildReceiptBody(ReceiptBody{
		Kind: ReceiptBodyBlockSync, PacketID: 17,
		Revision: -2147483648, PlusRevision: 2147483647,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(body)
	if got := raw.Lookup("method"); got.Type != 0 {
		t.Fatalf("method survived mapping: %v", got.Type)
	}
	if got := raw.Lookup("packetId"); got.Type != 0 {
		t.Fatalf("packetId survived mapping: %v", got.Type)
	}
	expected := map[string]int32{"r": -2147483648, "pr": 2147483647}
	elements, err := raw.Elements()
	if err != nil {
		t.Fatal(err)
	}
	if len(elements) != len(expected) {
		t.Fatalf("final keys=%d want exactly %d", len(elements), len(expected))
	}
	for _, element := range elements {
		want, ok := expected[element.Key()]
		if !ok {
			t.Fatalf("unexpected final key %q", element.Key())
		}
		value := element.Value()
		if value.Type != bson.TypeInt32 || len(value.Value) != 4 {
			t.Fatalf("%s=%v/%d bytes want BSON int32/4 bytes", element.Key(), value.Type, len(value.Value))
		}
		got := int32(binary.LittleEndian.Uint32(value.Value))
		if got != want {
			t.Fatalf("%s payload=%d want %d", element.Key(), got, want)
		}
	}
}

func TestBuildReceiptBodyBlockSyncPreservesZeroInt32Values(t *testing.T) {
	body, err := BuildReceiptBody(ReceiptBody{Kind: ReceiptBodyBlockSync, PacketID: 7})
	if err != nil {
		t.Fatal(err)
	}
	raw := bson.Raw(body)
	for _, key := range []string{"r", "pr"} {
		got := raw.Lookup(key)
		if got.Type != bson.TypeInt32 || got.Int32() != 0 {
			t.Fatalf("%s=%v/%d want BSON int32(0)", key, got.Type, got.Int32())
		}
	}
}

func TestBuildReceiptBodyRejectsUnknownKind(t *testing.T) {
	if _, err := BuildReceiptBody(ReceiptBody{Kind: 99}); err == nil {
		t.Fatal("unknown receipt body kind unexpectedly accepted")
	}
}
