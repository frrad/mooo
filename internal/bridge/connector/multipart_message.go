package connector

import (
	"context"
	"slices"
	"strconv"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"

	"github.com/frrad/mooo/internal/protocol/events"
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

// namedPartsMessage is a remote message mapped to a fixed set of named parts.
// Like an album, it resumes the parts still missing after a partially
// successful Matrix send instead of treating any existing part as complete.
type namedPartsMessage[T any] struct {
	*simplevent.Message[T]
	parts []networkid.PartID
}

func (m *namedPartsMessage[T]) ExpectedPartIDs() []networkid.PartID {
	return slices.Clone(m.parts)
}

// namedPartsEvent builds a namedPartsMessage. convert receives the parts
// already bridged and converts only the others.
func namedPartsEvent[T any](
	kc *KakaoClient,
	pos events.Position,
	authorID, sentAt int64,
	data T,
	parts []networkid.PartID,
	convert func(context.Context, *bridgev2.Portal, bridgev2.MatrixAPI, T, map[networkid.PartID]bool) (*bridgev2.ConvertedMessage, error),
) *namedPartsMessage[T] {
	meta := kc.messageMeta(pos.ChatID, pos.LogID, authorID, sentAt)
	meta.Type = bridgev2.RemoteEventMessageUpsert
	var existing map[networkid.PartID]bool
	remote := newMessage(meta, makeMessageID(pos.ChatID, pos.LogID), data,
		func(ctx context.Context, portal *bridgev2.Portal, intent bridgev2.MatrixAPI, value T) (*bridgev2.ConvertedMessage, error) {
			return convert(ctx, portal, intent, value, existing)
		})
	remote.HandleExistingFunc = func(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, rows []*database.Message, _ T) (bridgev2.UpsertResult, error) {
		existing = make(map[networkid.PartID]bool, len(rows))
		for _, row := range rows {
			existing[row.PartID] = true
		}
		for _, part := range parts {
			if !existing[part] {
				return bridgev2.UpsertResult{ContinueMessageHandling: true}, nil
			}
		}
		return bridgev2.UpsertResult{}, nil
	}
	return &namedPartsMessage[T]{Message: remote, parts: parts}
}
