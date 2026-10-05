package sessionlogin

import (
	"fmt"

	"github.com/frrad/mooo/internal/protocol/loco"
)

// ReceiptPacket is the explicit packet framing input. PacketID and Method are
// copied into the LOCO header as supplied; the signed owner admission tag is a
// separate concern and is intentionally absent here.
type ReceiptPacket struct {
	PacketID uint32
	Method   string
	Body     ReceiptBody
}

// BuildReceiptPacket builds one plaintext LOCO frame from an approved typed
// receipt body. It does not allocate request IDs, register pending requests,
// encrypt, submit, or activate any Session transport path. A zero maxBody uses
// loco's normal bounded default.
func BuildReceiptPacket(input ReceiptPacket, maxBody uint32) ([]byte, error) {
	bodyInput := input.Body
	bodyInput.PacketID = input.PacketID
	body, err := BuildReceiptBody(bodyInput)
	if err != nil {
		return nil, fmt.Errorf("sessionlogin: build receipt body: %w", err)
	}
	wire, err := (loco.Packet{
		Header: loco.Header{
			PacketID: input.PacketID,
			Status:   0,
			Method:   input.Method,
			BodyType: loco.BodyTypeBSON,
		},
		Body: body,
	}).MarshalBinary(maxBody)
	if err != nil {
		return nil, fmt.Errorf("sessionlogin: frame receipt packet: %w", err)
	}
	return wire, nil
}
