package chatmeta

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func richMeta(t *testing.T, content map[string]any) MoimMeta {
	t.Helper()
	raw, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	return MoimMeta{Type: MoimMetaNotice, UpdateRevision: 60, Content: string(raw)}
}

// Rich announcements summarize as the Mac banner does: structured text, then
// content, then subject, then a posted-media sentence.
func TestObservedRichAnnouncementsSummarizeLikeMacBanner(t *testing.T) {
	raw, err := os.ReadFile("../../../research/fixtures/chatmeta/observed-moim-rich-announcements.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name      string         `json:"name"`
			Content   map[string]any `json:"content"`
			MacBanner string         `json:"mac_banner"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) != 2 {
		t.Fatalf("fixture: %v", err)
	}
	for _, c := range fixture.Cases {
		a, err := richMeta(t, c.Content).Announcement()
		if err != nil || !a.Active || a.Text != c.MacBanner {
			t.Fatalf("%s: %+v %v, want %q", c.Name, a, err, c.MacBanner)
		}
	}
}

func TestRichAnnouncementFallbacks(t *testing.T) {
	cases := []struct {
		name    string
		content map[string]any
		want    string
	}{
		{"photo without text", map[string]any{"type": "IMAGE", "notice": true}, "The photo has been posted as an announcement."},
		{"video without text", map[string]any{"type": "VIDEO", "notice": true}, "The video has been posted as an announcement."},
		{"file without text", map[string]any{"type": "FILE", "notice": true}, "The file has been posted as an announcement."},
		{"poll with several items", map[string]any{"type": "POLL", "notice": true, "subject": "Synthetic poll", "poll_count": 3}, "Synthetic poll and 2 more"},
		{"structured text wins", map[string]any{"type": "TEXT", "notice": true, "content": "plain", "json_content": `[{"type":"text","text":"structured"}]`}, "structured"},
		{"unknown type with content", map[string]any{"type": "LINK", "notice": true, "content": "linked text"}, "linked text"},
	}
	for _, tc := range cases {
		a, err := richMeta(t, tc.content).Announcement()
		if err != nil || !a.Active || a.Text != tc.want {
			t.Fatalf("%s: %+v %v, want %q", tc.name, a, err, tc.want)
		}
	}
	// With nothing to show, the content is unsupported and the caller keeps
	// the current topic.
	if _, err := richMeta(t, map[string]any{"type": "LINK", "notice": true}).Announcement(); !errors.Is(err, ErrUnsupportedAnnouncement) {
		t.Fatalf("empty unknown announcement error = %v", err)
	}
}
