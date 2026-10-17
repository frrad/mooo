package connector

import (
	"context"
	"errors"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/messagetype"
)

var (
	// errLeverageReplyTarget refuses a Matrix reply to a locked or adult-only
	// card, on which the official client offers no Reply (research/leverage.md).
	errLeverageReplyTarget = errors.New("connector: reply target is a locked or adult-only KakaoTalk card")
	// errLeverageReactionTarget refuses a Matrix reaction to a carousel card,
	// on which the official client offers no reaction.
	errLeverageReactionTarget = errors.New("connector: reaction target is a KakaoTalk carousel card")
	// errLeverageDeleteNotSupported refuses deleting a card for everyone,
	// which is untraced in the official client.
	errLeverageDeleteNotSupported = errors.New("connector: KakaoTalk cards cannot be deleted from Matrix")
)

// convertLeverage posts a shared card as the text the official clients show
// for it outside the bubble (the chat log message), followed by the link the
// Mac opens when the card is tapped. The summary is also the reply preview,
// as in the official reply.
func convertLeverage(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, msg events.LeverageMessage) (*bridgev2.ConvertedMessage, error) {
	body := msg.Summary
	if msg.Link != "" {
		body += "\n" + msg.Link
	}
	metadata := newKakaoMessageMetadata(msg.ChatID, msg.LogID, msg.AuthorID, messagetype.Leverage, msg.Summary, 0)
	metadata.ReplyUnavailable = msg.ReplyUnavailable
	metadata.ReactionUnavailable = msg.ReactionUnavailable
	return messageWithMetadata(event.MsgText, body, metadata), nil
}
