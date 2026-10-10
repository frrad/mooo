package events

import (
	"encoding/json"
	"os"
	"testing"
)

func TestDecodeSyncedLogMetaObservedPage(t *testing.T) {
	raw, err := os.ReadFile("../../../research/fixtures/reactions/observed-sync-meta-page.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Page struct {
			Content []json.RawMessage `json:"content"`
		} `json:"page"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Page.Content) != 2 {
		t.Fatalf("fixture: %v", err)
	}
	legacy, err := DecodeSyncedLogMeta(fixture.Page.Content[0])
	if err != nil {
		t.Fatal(err)
	}
	l, ok := legacy.(ReactionChanged)
	// Large identifiers must survive exactly, not via float64.
	if !ok || l.MetadataType != 1 || l.ChatID != 3000 || l.LogID != 3948371787076642816 || l.Revision != 3948377092013952282 || len(l.Items) != 0 {
		t.Fatalf("legacy = %#v", legacy)
	}
	mini, err := DecodeSyncedLogMeta(fixture.Page.Content[1])
	if err != nil {
		t.Fatal(err)
	}
	m, ok := mini.(ReactionChanged)
	if !ok || m.MetadataType != 2 || m.Revision != 3948377398363489307 || len(m.Items) != 1 || m.Items[0].ID != "1200509_029" || m.Items[0].Kind != 2 {
		t.Fatalf("mini = %#v", mini)
	}
	if _, err := DecodeSyncedLogMeta([]byte(`{"chatId":1,"logId":2,"type":1,"revision":0,"content":"{}"}`)); err == nil {
		t.Fatal("non-positive revision accepted")
	}
}
