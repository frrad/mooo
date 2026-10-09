package connector

import (
	"context"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/messagetype"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"strconv"
	"strings"
)

func convertLocation(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, msg events.LocationMessage) (*bridgev2.ConvertedMessage, error) {
	body := msg.Address
	if strings.TrimSpace(msg.Title) != "" {
		body = msg.Title
		if msg.Address != "" {
			body += "\n" + msg.Address
		}
	}
	if strings.TrimSpace(body) == "" {
		body = "KakaoTalk location"
	}
	geo := "geo:" + strconv.FormatFloat(msg.Latitude, 'f', -1, 64) + "," + strconv.FormatFloat(msg.Longitude, 'f', -1, 64)
	metadata := newKakaoMessageMetadata(msg.ChatID, msg.LogID, msg.AuthorID, messagetype.Location, "[location]", 0)
	return &bridgev2.ConvertedMessage{Parts: []*bridgev2.ConvertedMessagePart{{Type: event.EventMessage, Content: &event.MessageEventContent{MsgType: event.MsgLocation, Body: body, GeoURI: geo}, DBMetadata: metadata}}}, nil
}
