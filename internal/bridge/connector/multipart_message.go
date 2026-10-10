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

// expectedPartsMessage is a remote message that declares every part it maps
// to, so a partially bridged message is not mistaken for a complete one.
type expectedPartsMessage interface {
	ExpectedPartIDs() []networkid.PartID
}

func (m *multipartMessage[T]) ExpectedPartIDs() []networkid.PartID {
	parts := make([]networkid.PartID, m.expectedParts)
	for i := range parts {
		parts[i] = networkid.PartID(strconv.Itoa(i))
	}
	return parts
}
