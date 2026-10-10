package connector

import (
	"strconv"

	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
)

// multipartMessage preserves the converter's upsert behavior and exposes the
// complete source part set for commit verification after suppressed SDK errors.
type multipartMessage[T any] struct {
	*simplevent.Message[T]
	expectedParts int
}

func (m *multipartMessage[T]) ExpectedPartIDs() []networkid.PartID {
	parts := make([]networkid.PartID, m.expectedParts)
	for i := range parts {
		parts[i] = networkid.PartID(strconv.Itoa(i))
	}
	return parts
}
