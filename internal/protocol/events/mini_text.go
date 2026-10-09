package events

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf16"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const KindMiniTextMessage Kind = "mini_text_message"

// MiniTextPart preserves a source fragment. A resource ID marks a graphic whose
// Text is its original fallback. Parts remain in source order.
type MiniTextPart struct{ Text, ResourceID string }

type MiniTextMessage struct {
	TextMessage
	Parts []MiniTextPart
}

func (MiniTextMessage) Kind() Kind         { return KindMiniTextMessage }
func (MiniTextMessage) isEvent()           {}
func (MiniTextMessage) String() string     { return "MiniTextMessage{<redacted>}" }
func (m MiniTextMessage) GoString() string { return m.String() }

func decodeMiniText(message TextMessage, log bson.Raw) (Event, error) {
	v, err := log.LookupErr("attachment")
	if err != nil || (v.Type == bson.TypeString && (strings.TrimSpace(v.StringValue()) == "" || v.StringValue() == "null")) {
		return message, nil
	}
	if v.Type != bson.TypeString || len(v.StringValue()) > 64<<10 {
		return nil, ErrMalformedEvent
	}
	var outer map[string]json.RawMessage
	if json.Unmarshal([]byte(v.StringValue()), &outer) != nil || outer == nil {
		return nil, ErrMalformedEvent
	}
	raw, ok := outer["emojis"]
	if !ok {
		return message, nil
	}
	if !validBoundedEventJSON(v.StringValue()) {
		return nil, ErrMalformedEvent
	}
	if len(message.Message) > 64<<10 {
		return nil, ErrMalformedEvent
	}
	var a struct {
		Count  *int `json:"total_item"`
		Length *int `json:"total_len"`
		Items  []struct {
			ID      string `json:"id"`
			Length  int    `json:"len"`
			Anchors []int  `json:"at"`
		} `json:"items"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if dec.Decode(&a) != nil || a.Count == nil || a.Length == nil || *a.Count < 0 || *a.Count > 128 || *a.Length < 0 || len(a.Items) > 32 {
		return nil, ErrMalformedEvent
	}
	units := utf16.Encode([]rune(message.Message))
	boundaries := map[int]int{0: 0}
	n := 0
	for i, r := range message.Message {
		boundaries[n] = i
		if r > 0xffff {
			n += 2
		} else {
			n++
		}
	}
	boundaries[n] = len(message.Message)
	var anchors []int
	for i, c := range units {
		if c == '(' {
			anchors = append(anchors, i)
		}
	}
	type span struct {
		start, end int
		id         string
	}
	var spans []span
	seen := map[int]bool{}
	length := 0
	for _, item := range a.Items {
		if !validMiniID(item.ID) || item.Length < 1 || item.Length > 12 || len(item.Anchors) == 0 {
			return nil, ErrMalformedEvent
		}
		for _, anchor := range item.Anchors {
			if anchor < 1 || anchor > len(anchors) || seen[anchor] || len(spans) >= 128 {
				return nil, ErrMalformedEvent
			}
			seen[anchor] = true
			start := anchors[anchor-1]
			end := start + item.Length
			bs, startOK := boundaries[start]
			be, endOK := boundaries[end]
			if !startOK || !endOK || end > len(units) {
				return nil, ErrMalformedEvent
			}
			spans = append(spans, span{bs, be, item.ID})
			length += item.Length
		}
	}
	if len(spans) != *a.Count || length != *a.Length {
		return nil, ErrMalformedEvent
	}
	if len(spans) == 0 {
		return message, nil
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	m := MiniTextMessage{TextMessage: message}
	end := 0
	for _, s := range spans {
		if s.start < end {
			return nil, ErrMalformedEvent
		}
		if s.start > end {
			m.Parts = append(m.Parts, MiniTextPart{Text: message.Message[end:s.start]})
		}
		m.Parts = append(m.Parts, MiniTextPart{Text: message.Message[s.start:s.end], ResourceID: s.id})
		end = s.end
	}
	if end < len(message.Message) {
		m.Parts = append(m.Parts, MiniTextPart{Text: message.Message[end:]})
	}
	return m, nil
}

func validMiniID(id string) bool {
	if len(id) < 4 || len(id) > 64 {
		return false
	}
	pieces := strings.Split(id, "_")
	if len(pieces) != 2 || len(pieces[0]) < 2 || pieces[1] == "" {
		return false
	}
	for _, piece := range pieces {
		for _, c := range piece {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}
