package sessionlogin

import (
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// ReceiptBodyKind identifies the reviewed push-receipt object shape. The
// packet header and admission tag are deliberately outside this type: neither
// is serialized into the BSON body.
type ReceiptBodyKind uint8

const (
	ReceiptBodyHint ReceiptBodyKind = iota + 1
	ReceiptBodyBlockSync
)

// ReceiptBody is the typed input to the reviewed push-receipt body builder.
// PacketID is retained only while projecting the source object; the observed
// static base fields are removed before encoding. Revision fields are encoded
// as BSON int32 values, including zero and signed boundary values.
type ReceiptBody struct {
	Kind         ReceiptBodyKind
	PacketID     uint32
	Revision     int32
	PlusRevision int32
}

var ErrInvalidReceiptBodyKind = errors.New("sessionlogin: invalid receipt body kind")

// BuildReceiptBody projects the source receipt object, applies the reviewed
// static-field removal and BLOCKSYNC renames, and encodes the resulting
// dictionary with the production BSON codec. HINT therefore produces the
// canonical empty BSON document. This function does not allocate a packet ID,
// construct a packet header, encrypt, submit, or touch pending-request state.
func BuildReceiptBody(input ReceiptBody) ([]byte, error) {
	var method string
	var names []string
	values := map[string]any{
		"packetId": input.PacketID,
	}
	switch input.Kind {
	case ReceiptBodyHint:
		method = "HINT"
		names = []string{"method", "packetId"}
	case ReceiptBodyBlockSync:
		method = "BLOCKSYNC"
		names = []string{"method", "packetId", "revision", "plusRevision"}
		values["revision"] = input.Revision
		values["plusRevision"] = input.PlusRevision
	default:
		return nil, fmt.Errorf("%w: %d", ErrInvalidReceiptBodyKind, input.Kind)
	}
	values["method"] = method

	projected, err := ProjectSGJSONNamedProperties(names, values)
	if err != nil {
		return nil, fmt.Errorf("sessionlogin: project receipt body: %w", err)
	}
	mappings := []ReceiptJSONMapping(nil)
	if input.Kind == ReceiptBodyBlockSync {
		mappings = []ReceiptJSONMapping{
			{Destination: "pr", Source: "plusRevision"},
			{Destination: "r", Source: "revision"},
		}
	}
	mapped := MapReceiptJSONObject(projected, []string{"method", "packetId"}, mappings)
	if mapped == nil {
		return nil, errors.New("sessionlogin: receipt body projection returned nil")
	}
	body, err := bson.Marshal(mapped)
	if err != nil {
		return nil, fmt.Errorf("sessionlogin: encode receipt body: %w", err)
	}
	return body, nil
}
