package loco

import (
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/frrad/mooo/internal/protocol/bsonfield"
)

// ErrMissingStatus means a reply body has no integer status field.
var ErrMissingStatus = errors.New("loco: reply status missing or malformed")

// Status reads the BSON status field every LOCO reply body carries. The field
// may be a BSON int32 or an int64 within int32 range; anything else, including
// an absent field, is ErrMissingStatus.
func Status(packet Packet) (int32, error) {
	status, ok, err := bsonfield.Reader{Err: ErrMissingStatus}.Int32(bson.Raw(packet.Body), "status")
	if err != nil || !ok {
		return 0, ErrMissingStatus
	}
	return status, nil
}
