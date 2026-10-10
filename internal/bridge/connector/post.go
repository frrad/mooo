package connector

import (
	"context"
	"fmt"
	"strings"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

func convertPost(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, m events.PostMessage) (*bridgev2.ConvertedMessage, error) {
	lines := []string{"KakaoTalk post:"}
	if m.Text != "" {
		lines = append(lines, m.Text)
	}
	switch {
	case m.Photos == 1:
		lines = append(lines, "[1 photo]")
	case m.Photos > 1:
		lines = append(lines, fmt.Sprintf("[%d photos]", m.Photos))
	}
	if m.PollTitle != "" {
		lines = append(lines, "Poll: "+m.PollTitle)
	}
	if m.Unrendered > 0 {
		lines = append(lines, fmt.Sprintf("(%d post item(s) not shown; open KakaoTalk to view)", m.Unrendered))
	}
	return messageWithMetadata(event.MsgText, strings.Join(lines, "\n"), newKakaoMessageMetadata(m.ChatID, m.LogID, m.AuthorID, messagetype.Post, "[post]", 0)), nil
}
