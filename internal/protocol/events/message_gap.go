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
	if envelopeErr != nil || duplicateKey(raw, "chatId") || duplicateKey(raw, "chatLog") || duplicateKey(raw, "logId") || duplicateKey(log, "logId") {
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
	// Duplicate non-identity fields make strict decoding ambiguous, but the
	// validated chat/log identity still permits an explicit, committable gap.
	// Do not retain optional attribution when its wire value is duplicated.
	if hasAnyDuplicateExcept(raw, "chatId", "chatLog") || hasAnyDuplicateExcept(log, "logId") {
		return MessageGap{ChatID: chatID, LogID: logID, Type: typ,
			AuthorID: uniqueOptionalInt64(log, "authorId"), SentAt: uniqueOptionalInt64(log, "sendAt")}, nil
	}
	result, err := Decode(packet)
	if err == nil {
		return result, nil
	}
	return MessageGap{ChatID: chatID, LogID: logID, Type: typ,
		AuthorID: uniqueOptionalInt64(log, "authorId"), SentAt: uniqueOptionalInt64(log, "sendAt")}, nil
}

func duplicateKey(raw bson.Raw, key string) bool {
	elements, err := raw.Elements()
	if err != nil {
		return true
	}
	count := 0
	for _, el := range elements {
		if el.Key() == key {
			count++
		}
	}
	return count > 1
}

func hasAnyDuplicateExcept(raw bson.Raw, exempt ...string) bool {
	elements, err := raw.Elements()
	if err != nil {
		return true
	}
	exemptSet := make(map[string]struct{}, len(exempt))
	for _, key := range exempt {
		exemptSet[key] = struct{}{}
	}
	seen := make(map[string]bool, len(elements))
	for _, el := range elements {
		key := el.Key()
		if _, ok := exemptSet[key]; ok {
			continue
		}
		if seen[key] {
			return true
		}
		seen[key] = true
	}
	return false
}

func uniqueOptionalInt64(raw bson.Raw, key string) int64 {
	if duplicateKey(raw, key) {
		return 0
	}
	return optionalInt64(raw, key)
}
