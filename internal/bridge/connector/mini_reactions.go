package connector

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/reactions"
)

// quickReactionEmoji maps the mini items behind Android's six quick
// reactions (observed, KakaoTalk Android 26.8.2) to the emoji the outbound
// legacy table uses, so Matrix shows the same reaction instead of a
// localized label.
var quickReactionEmoji = map[string]string{
	"1200509_029": "❤️",
	"1200509_021": "👍",
	"1200509_037": "✅",
	"1200509_001": "😆",
	"1200509_002": "😮",
	"1200509_003": "😢",
}

// Mini attribution has no revision field. Require exact agreement with the
// triggering aggregate before replacing any Matrix state. A racing snapshot
// fails without advancing the durable revision; the next push can reconcile.
func addMiniReactionUsers(users map[networkid.UserID]*bridgev2.ReactionSyncUser, details reactions.DetailsResponse, change events.ReactionChanged, self int64, login networkid.UserLoginID) error {
	if details.Status != 0 || details.Details == nil {
		return errReactionLookup
	}
	expected := make(map[string]events.ReactionItem)
	for _, item := range change.Items {
		if item.Kind != 2 {
			continue
		}
		if item.ID == "" || len(item.ID) > 128 || strings.ContainsAny(item.ID, "\x00\r\n") || item.Count < 0 {
			return errReactionUnsupported
		}
		if _, duplicate := expected[item.ID]; duplicate {
			return errReactionUnsupported
		}
		expected[item.ID] = item
	}
	seen := make(map[string]bool)
	for _, detail := range details.Details {
		if detail.Kind != 1 && detail.Kind != 2 {
			return errReactionUnsupported
		}
		if detail.Kind != 2 {
			continue
		}
		item, ok := expected[detail.ReactionID]
		if !ok || seen[detail.ReactionID] {
			return errReactionRevision
		}
		seen[detail.ReactionID] = true
		actors := make(map[int64]bool)
		for _, actor := range detail.UserIDs {
			if actor <= 0 || actors[actor] {
				return errReactionUnsupported
			}
			actors[actor] = true
		}
		if int64(len(actors)) != item.Count {
			return errReactionRevision
		}
		label := quickReactionEmoji[item.ID]
		if label == "" {
			label = item.Alt["en"]
		}
		if label == "" {
			keys := make([]string, 0, len(item.Alt))
			for k := range item.Alt {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if item.Alt[k] != "" {
					label = item.Alt[k]
					break
				}
			}
		}
		if label == "" && detail.ItemMeta != nil {
			label = detail.ItemMeta.Title
		}
		if label == "" {
			label = fmt.Sprintf("Kakao reaction %s", item.ID)
		}
		if !utf8.ValidString(label) || len(label) > 512 || strings.ContainsAny(label, "\x00\r\n") {
			return errReactionUnsupported
		}
		for actor := range actors {
			id := makeUserID(actor)
			user := users[id]
			if user == nil {
				user = &bridgev2.ReactionSyncUser{HasAllReactions: true}
				users[id] = user
			}
			sender := bridgev2.EventSender{Sender: id}
			if actor == self {
				sender.IsFromMe = true
				sender.SenderLogin = login
			}
			user.Reactions = append(user.Reactions, &bridgev2.BackfillReaction{Sender: sender, EmojiID: networkid.EmojiID("kakao:mini:" + item.ID), Emoji: label})
		}
	}
	for key, item := range expected {
		if item.Count > 0 && !seen[key] {
			return errReactionRevision
		}
	}
	return nil
}
