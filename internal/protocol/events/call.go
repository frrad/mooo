package events

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/frrad/mooo/internal/protocol/chatlog"
)

// CallMessage is a voice or video call record (chatLog type 51). Each call
// writes a start record and an outcome record, both authored by the caller
// (research/calls.md). Only the event type, duration and joined members are
// kept: the chat log's message field duplicates the attachment, which also
// carries call-server addresses and a call token.
type CallMessage struct {
	ChatID, LogID, AuthorID, SentAt int64
	// Event is the attachment's type string when the official client
	// recognizes it (for example "invite", "v_bye"), and empty otherwise.
	Event string
	// Duration is in seconds, read the way the official model reads it.
	Duration int64
	// Members are the user IDs of an "add" or "v_add" record.
	Members []int64
}

func (CallMessage) Kind() Kind           { return KindCallMessage }
func (m CallMessage) Position() Position { return Position{m.ChatID, m.LogID} }
func (CallMessage) isEvent()             {}
func (CallMessage) String() string       { return "CallMessage{<redacted>}" }
func (m CallMessage) GoString() string   { return m.String() }

// callEvents are the type strings the official client maps to a call event.
var callEvents = map[string]bool{
	"maintenance": true, "transferred": true, "v_transferred": true,
	"invite": true, "cinvite": true, "deny": true, "noanswer": true, "canceled": true, "bye": true, "add": true,
	"v_invite": true, "v_cinvite": true, "v_deny": true, "v_noanswer": true, "v_canceled": true, "v_bye": true, "v_add": true,
}

const (
	maxCallAttachment = 64 << 10
	maxCallMembers    = 1000
)

// decodeCall never fails: like the official client, a record whose
// attachment is missing or unreadable is an unrecognized call event. Its
// content is never shown, so it is not a delivery gap.
func decodeCall(env chatlog.Envelope) (Event, error) {
	call := CallMessage{ChatID: env.ChatID, LogID: env.LogID, AuthorID: env.AuthorID, SentAt: env.SentAt}
	value, ok := env.Log.Lookup("attachment").StringValueOK()
	if !ok || len(value) > maxCallAttachment || !utf8.ValidString(value) {
		return call, nil
	}
	var a map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	if decoder.Decode(&a) != nil || a == nil {
		return call, nil
	}
	var event string
	if json.Unmarshal(a["type"], &event) == nil && callEvents[event] {
		call.Event = event
	}
	call.Duration = callInteger(a["duration"])
	if call.Event == "add" || call.Event == "v_add" {
		call.Members = callMembers(a["member"])
	}
	return call, nil
}

// callInteger reads a JSON value as the official model's integer property
// does: numbers truncate toward zero, numeric strings are read as
// longLongValue reads them, and anything else is zero.
func callInteger(raw json.RawMessage) int64 {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return 0
	}
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return 0
		}
		return leadingInteger(s)
	case 't':
		if string(raw) == "true" {
			return 1
		}
		return 0
	case 'f', 'n', '[', '{':
		return 0
	}
	if n, err := strconv.ParseInt(string(raw), 10, 64); err == nil {
		return n
	}
	f, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || math.IsNaN(f) || f >= math.MaxInt64 || f <= math.MinInt64 {
		return 0
	}
	return int64(f)
}

// leadingInteger reads an optionally signed decimal prefix after leading
// whitespace, saturating on overflow, and is zero without digits.
func leadingInteger(s string) int64 {
	s = strings.TrimLeft(s, " \t\n\r\v\f")
	negative := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		s = s[1:]
	}
	var n int64
	for i := 0; i < len(s) && s[i] >= '0' && s[i] <= '9'; i++ {
		d := int64(s[i] - '0')
		if n > (math.MaxInt64-d)/10 {
			if negative {
				return math.MinInt64
			}
			return math.MaxInt64
		}
		n = n*10 + d
	}
	if negative {
		return -n
	}
	return n
}

// callMembers reads positive user IDs given as numbers or numeric strings.
func callMembers(raw json.RawMessage) []int64 {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var members []int64
	for _, item := range items {
		item = bytes.TrimSpace(item)
		if len(item) == 0 || (item[0] != '"' && item[0] != '-' && (item[0] < '0' || item[0] > '9')) {
			continue
		}
		if id := callInteger(item); id > 0 && len(members) < maxCallMembers {
			members = append(members, id)
		}
	}
	return members
}
