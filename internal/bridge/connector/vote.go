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

func convertVote(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, m events.VoteMessage) (*bridgev2.ConvertedMessage, error) {
	var body strings.Builder
	body.WriteString("KakaoTalk poll: ")
	body.WriteString(m.Title)
	for i, option := range m.Options {
		fmt.Fprintf(&body, "\n%d. %s", i+1, option)
	}
	return messageWithMetadata(event.MsgText, body.String(), newKakaoMessageMetadata(m.ChatID, m.LogID, m.AuthorID, messagetype.Vote, "[poll]", 0)), nil
}
