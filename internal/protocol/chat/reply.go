package chat

import (
	"encoding/json"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	ReplyType            = int32(26)
	maxReplyPreviewUTF16 = 100
)

// ReplyTarget identifies the source message represented in a reply attachment.
type ReplyTarget struct {
	LogID   int64
	UserID  int64
	LinkID  int64
	Type    int32
	Message string
}

func (t ReplyTarget) validate() error {
	if t.LogID <= 0 || t.UserID <= 0 || t.LinkID < 0 || t.Type <= 0 {
		return ErrInvalidMessage
	}
	if !utf8.ValidString(t.Message) || len(t.Message) > maxTextBytes || containsNUL(t.Message) {
		return ErrInvalidMessage
	}
	return nil
}

// ReplyRequest is encoded as a type-26 WRITE with source metadata in extra.
type ReplyRequest struct {
	ChatID  int64
	Message string
	Target  ReplyTarget
}

func (r ReplyRequest) MarshalBSON() ([]byte, error) {
	if r.Target.validate() != nil {
		return nil, ErrInvalidMessage
	}
	attachment := struct {
		SourceLogID    int64  `json:"src_logId"`
		SourceUserID   int64  `json:"src_userId"`
		SourceLinkID   int64  `json:"src_linkId,omitempty"`
		SourceType     int32  `json:"src_type"`
		SourceMessage  string `json:"src_message"`
		SourceSpoilers []any  `json:"src_spoilers"`
	}{
		SourceLogID: r.Target.LogID, SourceUserID: r.Target.UserID,
		SourceLinkID: r.Target.LinkID, SourceType: r.Target.Type,
		SourceMessage: truncateReplyPreview(r.Target.Message), SourceSpoilers: []any{},
	}
	extra, err := json.Marshal(attachment)
	if err != nil {
		return nil, ErrInvalidMessage
	}
	return (WriteRequest{
		ChatID: r.ChatID, Message: r.Message, Type: ReplyType, Extra: string(extra),
	}).MarshalBSON()
}

func truncateReplyPreview(message string) string {
	units := utf16.Encode([]rune(message))
	if len(units) <= maxReplyPreviewUTF16 {
		return message
	}
	units = units[:maxReplyPreviewUTF16]
	if last := units[len(units)-1]; last >= 0xd800 && last <= 0xdbff {
		units = units[:len(units)-1]
	}
	return string(utf16.Decode(units))
}

func containsNUL(value string) bool {
	for _, r := range value {
		if r == 0 {
			return true
		}
	}
	return false
}
