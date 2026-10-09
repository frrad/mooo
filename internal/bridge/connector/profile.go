package connector

import (
	"context"
	"fmt"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

func convertProfile(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, m events.ProfileMessage) (*bridgev2.ConvertedMessage, error) {
	body := fmt.Sprintf("KakaoTalk profile: %s\nKakaoTalk user ID: %d", m.NickName, m.UserID)
	if m.StatusMessage != "" {
		body += "\n" + m.StatusMessage
	}
	return messageWithMetadata(event.MsgText, body, newKakaoMessageMetadata(m.ChatID, m.LogID, m.AuthorID, messagetype.Profile, "[profile]", 0)), nil
}
