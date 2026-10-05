package sessionlogin

import (
	"bytes"
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
		Revision: -1, PlusRevision: 2147483647,
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
	for key, want := range map[string]int32{"r": -1, "pr": 2147483647} {
		got := raw.Lookup(key)
		if got.Type != bson.TypeInt32 || got.Int32() != want {
			t.Fatalf("%s=%v/%d want BSON int32(%d)", key, got.Type, got.Int32(), want)
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
