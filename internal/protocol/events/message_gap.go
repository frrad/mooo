package events

import (
	"github.com/frrad/mooo/internal/protocol/loco"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// MessageGap retains a validated message identity whose content cannot be
// parsed. Consumers must persist an explicit gap before committing this event.
// No malformed content, attachment, URL, or parser error is retained.
type MessageGap struct {
	ChatID, LogID, AuthorID, SentAt int64
	Type                            int32
}

func (MessageGap) Kind() Kind { return KindMessageGap }
func (MessageGap) isEvent()   {}

// DecodeForDelivery preserves deterministic content failures as explicit gaps.
// Decode remains the strict protocol parser. An invalid or ambiguous identity
// remains an error: callers must stop message admission rather than skip it.
func DecodeForDelivery(packet loco.Packet) (Event, error) {
	if packet.Header.Method != "MSG" {
		return Decode(packet)
	}
	raw := bson.Raw(packet.Body)
	chatID, logID, typ, log, envelopeErr := messageEnvelope(raw)
	if envelopeErr != nil || !uniqueKeys(raw) || !uniqueKeys(log) {
		return nil, ErrUnidentifiableMessage
	}
	// A malformed inner ID must not be replaced by an outer fallback.
	if _, lookupErr := log.LookupErr("logId"); lookupErr == nil {
		inner, e := requiredInt64(log, "logId")
		if e != nil || inner != logID {
			return nil, ErrUnidentifiableMessage
		}
	}
	// If both locations carry an ID, disagreement is not a recoverable identity.
	if _, lookupErr := raw.LookupErr("logId"); lookupErr == nil {
		outer, e := requiredInt64(raw, "logId")
		if e != nil || outer != logID {
			return nil, ErrUnidentifiableMessage
		}
	}
	result, err := Decode(packet)
	if err == nil {
		return result, nil
	}
	return MessageGap{ChatID: chatID, LogID: logID, Type: typ,
		AuthorID: optionalInt64(log, "authorId"), SentAt: optionalInt64(log, "sendAt")}, nil
}

func uniqueKeys(raw bson.Raw) bool {
	elements, err := raw.Elements()
	if err != nil {
		return false
	}
	seen := make(map[string]bool, len(elements))
	for _, el := range elements {
		key := el.Key()
		if seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}
