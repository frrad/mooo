package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/messagetype"
)

var (
	// errCallReplyTarget refuses a Matrix reply to a call record: the
	// official reply shape for one is untraced (research/calls.md).
	errCallReplyTarget = errors.New("connector: reply target is a KakaoTalk call record")
	// errCallDeleteNotSupported refuses deleting a call record for everyone,
	// which is untraced in the official client.
	errCallDeleteNotSupported = errors.New("connector: KakaoTalk call records cannot be deleted from Matrix")
)

// callQuote replaces the embedded source text of a reply to a call record:
// that text is the record's attachment JSON, not display text.
const callQuote = "KakaoTalk call"

// convertCall posts the official Mac chat-list summary of a call record as a
// notice. "Viewer" in that text is the bridged account.
func (kc *KakaoClient) convertCall(ctx context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, msg events.CallMessage) (*bridgev2.ConvertedMessage, error) {
	body := callSummary(msg, msg.AuthorID == kc.userID, func(userID int64) string { return kc.callMemberName(ctx, userID) })
	metadata := newKakaoMessageMetadata(msg.ChatID, msg.LogID, msg.AuthorID, messagetype.Mvoip, "", 0)
	return messageWithMetadata(event.MsgNotice, body, metadata), nil
}

// callSummary is the Mac client's English chat-list text for a call record
// (research/fixtures/calls/executed-mac-text.json). Group and direct rooms
// use different keys with identical English text.
func callSummary(msg events.CallMessage, viewerAuthored bool, name func(int64) string) string {
	medium := "Voice Call"
	if strings.HasPrefix(msg.Event, "v_") {
		medium = "Video Call"
	}
	switch strings.TrimPrefix(msg.Event, "v_") {
	case "invite", "cinvite":
		return medium
	case "canceled":
		if viewerAuthored {
			return "Cancelled " + medium
		}
		return "Missed " + medium
	case "deny", "noanswer":
		if viewerAuthored {
			return "No Answer on " + medium
		}
		return "Missed " + medium
	case "bye":
		return medium + " " + callDuration(msg.Duration)
	case "add":
		names := make([]string, len(msg.Members))
		for i, userID := range msg.Members {
			names[i] = name(userID)
		}
		return strings.Join(names, ", ") + " joined the " + medium
	case "transferred":
		return "Switch to Group " + medium + "."
	case "maintenance":
		return "The service is currently under maintenance. We apologize for any inconvenience."
	}
	return "Please answer the call on your mobile."
}

// callDuration formats seconds as m:ss below one hour and h:mm:ss from one
// hour, taking the official client's branches (a negative value skips both).
func callDuration(seconds int64) string {
	if seconds >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds%3600/60, seconds%60)
	}
	minutes := int64(0)
	if seconds >= 60 {
		minutes, seconds = seconds/60, seconds%60
	}
	return fmt.Sprintf("%d:%02d", minutes, seconds)
}

// callMemberName is a user's stored nickname, or "(Unknown)" as in the
// official client. The bridged account has no stored nickname; its login
// name is a placeholder carrying the account ID, so it is not used.
func (kc *KakaoClient) callMemberName(ctx context.Context, userID int64) string {
	record, _, err := kc.readMemberProfile(ctx, userID)
	if err == nil && record.Profile != nil && record.Profile.Nickname != "" {
		return record.Profile.Nickname
	}
	return "(Unknown)"
}
