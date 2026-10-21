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
// a rich card. Carousel Feed content is retained for the bridge.
type LeverageMessage struct {
	ChatID, LogID, AuthorID, SentAt int64
	// Summary is the chat log's message field.
	Summary string
	Ad      bool
	Cards   []LeverageCard
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
	unsupported := UnsupportedMessage{ChatID: env.ChatID, LogID: env.LogID, Type: env.Type, AuthorID: env.AuthorID, SentAt: env.SentAt}
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
	card.Ad = leverageBool(public["AD"])
	if card.ReactionUnavailable {
		card.Cards = leverageCarousel(attachment["C"])
	}
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

// LeverageCard is the bounded, displayable subset of a Carousel/Feed item.
// It excludes signatures, vendor data and executable app actions.
type LeverageCard struct {
	Heading, Description string
	Images               []LeverageImage
	Actions              []LeverageAction
}
type LeverageImage struct {
	URL           string
	Width, Height int
	Action        LeverageAction
}
type LeverageAction struct {
	Label, URL  string
	OpenInKakao bool
}

func leverageObject(raw json.RawMessage) map[string]json.RawMessage {
	var value map[string]json.RawMessage
	_ = json.Unmarshal(raw, &value)
	return value
}
func leverageText(raw json.RawMessage) string {
	s := leverageString(raw)
	if len(s) > maxLeverageText || !validEventString(s) {
		return ""
	}
	return s
}
func leverageAction(raw json.RawMessage) LeverageAction {
	link := leverageObject(raw)
	a := LeverageAction{URL: leverageLink(raw)}
	if leverageBool(link["LAA"]) {
		return LeverageAction{}
	}
	// Mobile web is a bridge fallback, not the Mac client's tap policy.
	if a.URL == "" && leverageString(link["LCM"]) == "" && leverageString(link["LPC"]) == "" {
		if mobile := leverageString(link["LMO"]); mobile != "" {
			b, _ := json.Marshal(map[string]json.RawMessage{"LPC": link["LMO"], "LAT": link["LAT"]})
			a.URL = leverageLink(b)
		}
	}
	a.OpenInKakao = a.URL == "" && (leverageString(link["LCA"]) != "" || leverageString(link["LCI"]) != "")
	return a
}
func leverageCarousel(raw json.RawMessage) []LeverageCard {
	content := leverageObject(raw)
	if !strings.EqualFold(leverageString(content["CTP"]), "Feed") {
		return nil
	}
	var items []json.RawMessage
	if json.Unmarshal(content["CIL"], &items) != nil || len(items) > 10 {
		return nil
	}
	cards := make([]LeverageCard, 0, len(items))
	for _, item := range items {
		fields := leverageObject(item)
		if fields == nil {
			continue
		}
		text := func(key string) string { return leverageText(leverageObject(leverageObject(fields[key])["TD"])["T"]) }
		c := LeverageCard{Heading: text("HD"), Description: text("TI")}
		var images []json.RawMessage
		_ = json.Unmarshal(fields["THL"], &images)
		if len(images) > 3 {
			images = images[:3]
		}
		for _, rawImage := range images {
			img := leverageObject(rawImage)
			th := leverageObject(img["TH"])
			// Reuse the plain-web validator; download policy is stricter at transfer.
			b, _ := json.Marshal(map[string]json.RawMessage{"LPC": th["THU"]})
			u := leverageLink(b)
			if u == "" {
				continue
			}
			var w, h int
			_ = json.Unmarshal(th["W"], &w)
			_ = json.Unmarshal(th["H"], &h)
			if w < 0 || w > 8192 {
				w = 0
			}
			if h < 0 || h > 8192 {
				h = 0
			}
			c.Images = append(c.Images, LeverageImage{URL: u, Width: w, Height: h, Action: leverageAction(img["L"])})
		}
		var buttons []json.RawMessage
		_ = json.Unmarshal(fields["BUL"], &buttons)
		if len(buttons) > 5 {
			buttons = buttons[:5]
		}
		for _, button := range buttons {
			b := leverageObject(button)
			a := leverageAction(b["L"])
			a.Label = leverageText(leverageObject(b["BU"])["T"])
			if a.Label != "" {
				c.Actions = append(c.Actions, a)
			}
		}
		cards = append(cards, c)
	}
	return cards
}
