package connector

import (
	"context"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

func convertPost(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, m events.PostMessage) (*bridgev2.ConvertedMessage, error) {
	return messageWithMetadata(event.MsgText, "KakaoTalk post:\n"+m.Text, newKakaoMessageMetadata(m.ChatID, m.LogID, m.AuthorID, messagetype.Post, "[post]", 0)), nil
}
