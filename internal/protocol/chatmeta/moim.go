package chatmeta

import (
	"encoding/json"
	"errors"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	"strings"
)

// Moim metadata belongs to Boards, independently of shared chat metadata.
const GetMoimMetaCommand = "GETMOMETA"
const MoimMetaNotice int32 = 1

var ErrUnsupportedAnnouncement = errors.New("chatmeta: unsupported announcement content")

type MoimRequest struct{ ChatID int64 }

func (r MoimRequest) MarshalBSON() ([]byte, error) {
	if r.ChatID <= 0 {
		return nil, ErrInvalidRequest
	}
	return bson.Marshal(bson.D{{Key: "c", Value: r.ChatID}, {Key: "ts", Value: bson.A{MoimMetaNotice}}})
}

type MoimResponse struct {
	ChatID int64
	Metas  []MoimMeta
}
type MoimMeta struct {
	Type                          int32
	UpdateRevision, BadgeRevision int64
	Content                       string
}

// DecodeMoimResponse accepts the Mac response mappings and push property names.
func DecodeMoimResponse(body []byte) (MoimResponse, error) {
	raw := bson.Raw(body)
	if raw.Validate() != nil {
		return MoimResponse{}, ErrInvalidResponse
	}
	var r MoimResponse
	var err error
	var ok bool
	r.ChatID, ok, err = int64Field(raw, "c", "chatId")
	if err != nil || !ok || r.ChatID <= 0 {
		return r, fmt.Errorf("%w: moim chat id", ErrInvalidResponse)
	}
	v, ok, err := lookup(raw, "ms", "metas")
	if err != nil {
		return r, err
	}
	if !ok || v.Type != bson.TypeArray {
		return r, fmt.Errorf("%w: moim metas", ErrInvalidResponse)
	}
	values, err := v.Array().Values()
	if err != nil || len(values) > 128 {
		return r, ErrInvalidResponse
	}
	for _, v := range values {
		if v.Type != bson.TypeEmbeddedDocument {
			return r, ErrInvalidResponse
		}
		d := v.Document()
		var m MoimMeta
		m.Type, ok, err = int32Field(d, "t", "type")
		if err != nil || !ok {
			return r, ErrInvalidResponse
		}
		m.UpdateRevision, ok, err = int64Field(d, "ur", "updateRevision")
		if err != nil || !ok || m.UpdateRevision < 0 {
			return r, ErrInvalidResponse
		}
		m.BadgeRevision, _, err = int64Field(d, "br", "badgeRevision")
		if err != nil || m.BadgeRevision < 0 {
			return r, ErrInvalidResponse
		}
		m.Content, _, err = stringField(d, "ct", "content")
		if err != nil || len(m.Content) > 1<<20 {
			return r, ErrInvalidResponse
		}
		r.Metas = append(r.Metas, m)
	}
	return r, nil
}

type Announcement struct {
	Active bool
	Text   string
}

func (m MoimMeta) Announcement() (Announcement, error) {
	if m.Type != MoimMetaNotice {
		return Announcement{}, fmt.Errorf("%w: not a notice", ErrInvalidResponse)
	}
	if m.Content == "" {
		return Announcement{}, nil
	}
	var p struct {
		Type        string `json:"type"`
		Notice      bool   `json:"notice"`
		Content     string `json:"content"`
		JSONContent string `json:"json_content"`
		Subject     string `json:"subject"`
		PollCount   int64  `json:"poll_count"`
	}
	if json.Unmarshal([]byte(m.Content), &p) != nil {
		return Announcement{}, fmt.Errorf("%w: announcement JSON", ErrInvalidResponse)
	}
	if !p.Notice {
		return Announcement{}, nil
	}
	text := announcementSummary(p.Type, p.JSONContent, p.Content, p.Subject, p.PollCount)
	if text == "" {
		// The official client shows an update prompt here; the bridge keeps
		// the current topic instead.
		return Announcement{}, ErrUnsupportedAnnouncement
	}
	return Announcement{Active: true, Text: text}, nil
}

// announcementSummary follows the Mac banner order: structured text, then
// content, then subject (a poll post with several polls adds a count), then
// a sentence for posted media.
func announcementSummary(postType, structured, content, subject string, pollCount int64) string {
	if text := structuredAnnouncementText(structured); text != "" {
		return text
	}
	if content != "" {
		return content
	}
	if subject != "" {
		if postType == "POLL" && pollCount >= 2 {
			return fmt.Sprintf("%s and %d more", subject, pollCount-1)
		}
		return subject
	}
	switch postType {
	case "IMAGE":
		return "The photo has been posted as an announcement."
	case "VIDEO":
		return "The video has been posted as an announcement."
	case "FILE":
		return "The file has been posted as an announcement."
	}
	return ""
}

// structuredAnnouncementText joins the text of json_content elements.
// Malformed structured content falls back to the plain fields.
func structuredAnnouncementText(structured string) string {
	if len(structured) <= 2 {
		return ""
	}
	var elements []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal([]byte(structured), &elements) != nil {
		return ""
	}
	var b strings.Builder
	for _, e := range elements {
		b.WriteString(e.Text)
	}
	return b.String()
}
