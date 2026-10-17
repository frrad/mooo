package events

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/frrad/mooo/internal/protocol/chatlog"
)

// LeverageMessage is a shared card (chatLog type 71): a KakaoTalk channel
// profile or post, or another service's share template (research/leverage.md).
// The official clients show the chat log's message as the card's text in the
// chat list, the reply preview and a reply's src_message; the bubble itself is
// a rich card. Only that text, the card's web link and the reply rule are kept.
type LeverageMessage struct {
	ChatID, LogID, AuthorID, SentAt int64
	// Summary is the chat log's message field.
	Summary string
	// Link is the http(s) URL the Mac client opens when the card is tapped,
	// or empty when there is none or it cannot be opened without the official
	// client (adult verification, authorization, a warning dialog).
	Link string
	// ReplyUnavailable is set for locked and adult-only cards, on which the
	// official client offers no Reply.
	ReplyUnavailable bool
	// ReactionUnavailable is set for carousel cards, on which the official
	// client offers no reaction.
	ReactionUnavailable bool
}

func (LeverageMessage) Kind() Kind           { return KindLeverageMessage }
func (m LeverageMessage) Position() Position { return Position{m.ChatID, m.LogID} }
func (LeverageMessage) isEvent()             {}
func (LeverageMessage) String() string       { return "LeverageMessage{<redacted>}" }
func (m LeverageMessage) GoString() string   { return m.String() }

const (
	maxLeverageText       = 64 << 10
	maxLeverageAttachment = 64 << 10
	maxLeverageLink       = 2048
)

// decodeLeverage never fails. A card whose text cannot be shown, or whose
// summary comes from the untraced calendar formatter, stays an unsupported
// notice. An unreadable attachment only loses the link, as in the official
// client, whose summary does not depend on it.
func decodeLeverage(env chatlog.Envelope) (Event, error) {
	unsupported := UnsupportedMessage{ChatID: env.ChatID, LogID: env.LogID, Type: env.Type}
	message, ok := env.Log.Lookup("message").StringValueOK()
	if !ok || len(message) > maxLeverageText || !validEventString(message) || strings.TrimSpace(message) == "" {
		return unsupported, nil
	}
	card := LeverageMessage{ChatID: env.ChatID, LogID: env.LogID, AuthorID: env.AuthorID, SentAt: env.SentAt, Summary: message}
	value, ok := env.Log.Lookup("attachment").StringValueOK()
	if !ok || len(value) > maxLeverageAttachment || !utf8.ValidString(value) {
		return card, nil
	}
	var attachment map[string]json.RawMessage
	if decodeSingleJSON(value, &attachment) != nil {
		return card, nil
	}
	if calendar, ok := attachment["CAL"]; ok && string(bytes.TrimSpace(calendar)) != "null" {
		return unsupported, nil
	}
	var public map[string]json.RawMessage
	if json.Unmarshal(attachment["P"], &public) != nil {
		return card, nil
	}
	// The Mac lower-cases TP to pick the layout. Its other no-reaction case,
	// a "growable" card, exists only in Plus chats (research/leverage.md).
	card.ReactionUnavailable = strings.ToLower(leverageString(public["TP"])) == "carousel"
	locked, adult := leverageBool(public["LOCK"]), leverageBool(public["ADULT"])
	card.ReplyUnavailable = locked || adult
	// The Mac shows no link on mobile-only (PC false), alimtalk, locked and
	// adult-gated cards, and asks before opening a soft-warning (SW) one.
	desktop := len(public["PC"]) == 0 || string(public["PC"]) == "null" || leverageBool(public["PC"])
	if !desktop || locked || adult || leverageBool(public["ALM"]) || leverageBool(public["SW"]) {
		return card, nil
	}
	card.Link = leverageLink(public["L"])
	return card, nil
}

// leverageLink picks the link object's Mac URL (LCM), else its PC URL (LPC),
// when its action type (LAT, "web" when absent or empty) contains "web". An
// authorized (LAA) link is opened by the official client with account
// authorization, so it is not bridged; neither is anything but a plain
// http(s) URL.
func leverageLink(raw json.RawMessage) string {
	var link map[string]json.RawMessage
	if json.Unmarshal(raw, &link) != nil || leverageBool(link["LAA"]) {
		return ""
	}
	if action, present := link["LAT"]; present && string(action) != "null" {
		var value string
		if json.Unmarshal(action, &value) != nil || value != "" && !strings.Contains(strings.ToLower(value), "web") {
			return ""
		}
	}
	target := leverageString(link["LCM"])
	if target == "" {
		target = leverageString(link["LPC"])
	}
	if len(target) > maxLeverageLink {
		return ""
	}
	for i := 0; i < len(target); i++ {
		if target[i] <= ' ' || target[i] >= 0x7f {
			return ""
		}
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Host == "" || parsed.Opaque != "" {
		return ""
	}
	if scheme := strings.ToLower(parsed.Scheme); scheme != "http" && scheme != "https" {
		return ""
	}
	return target
}

func leverageString(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

// leverageBool reads a value as NSNumber or NSString boolValue does: a
// number is true when non-zero; a string, after leading whitespace, a sign
// and leading zeros, is true when it starts with Y, y, T, t or a digit 1-9.
func leverageBool(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return false
	}
	switch raw[0] {
	case 't':
		return string(raw) == "true"
	case 'f', 'n', '[', '{':
		return false
	case '"':
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return false
		}
		s = strings.TrimLeft(s, " \t\n\r\v\f")
		if s != "" && (s[0] == '+' || s[0] == '-') {
			s = s[1:]
		}
		s = strings.TrimLeft(s, "0")
		return s != "" && strings.ContainsRune("YyTt123456789", rune(s[0]))
	}
	f, err := strconv.ParseFloat(string(raw), 64)
	return err == nil && f != 0
}
