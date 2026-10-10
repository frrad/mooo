package chatmeta

import (
	"encoding/json"
	"os"
	"testing"
)

func TestExecutedGroupDisplayFixture(t *testing.T) {
	raw, err := os.ReadFile("../../../research/fixtures/group-metadata/display.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Input struct {
				Name          string
				PersonalName  string
				PersonalImage string
				R2            int64
				R3            int64
				R4            int64
				Title         *string
				Image         *string
				Full          *string
			}
			DisplayTitle     string
			DisplayImage     string
			DisplayFullImage string
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, c := range fixture.Cases {
		t.Run(c.Input.Name, func(t *testing.T) {
			title, image, full := "Shared", "https://example.invalid/shared-small.png", "https://example.invalid/shared-full.png"
			if c.Input.Title != nil {
				title = *c.Input.Title
			}
			if c.Input.Image != nil {
				image = *c.Input.Image
			}
			if c.Input.Full != nil {
				full = *c.Input.Full
			}
			legacyJSON, _ := json.Marshal(map[string]string{"group_name": "Legacy", "group_profile_thumbnail_url": "https://example.invalid/legacy-small.png", "group_profile_url": "https://example.invalid/legacy-full.png"})
			profileJSON, _ := json.Marshal(map[string]string{"imageUrl": image, "fullImageUrl": full})
			data := ChatData{Type: "MultiChat", Meta: &RoomMeta{Name: c.Input.PersonalName, ImageURL: c.Input.PersonalImage, FullImageURL: c.Input.PersonalImage}, ChatMetas: []ChatMeta{{Type: SharedMetaKakaoGroup, Revision: c.Input.R2, Content: string(legacyJSON)}, {Type: SharedMetaTitle, Revision: c.Input.R3, Content: title}, {Type: SharedMetaProfile, Revision: c.Input.R4, Content: string(profileJSON)}}}
			got, err := ProjectGroupDisplay(data)
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != c.DisplayTitle || got.ImageURL != c.DisplayImage || got.FullImageURL != c.DisplayFullImage || !got.AvatarKnown {
				t.Fatalf("display = %+v, want %q/%q/%q", got, c.DisplayTitle, c.DisplayImage, c.DisplayFullImage)
			}
		})
	}
}

func TestExecutedProfileContentFixture(t *testing.T) {
	raw, err := os.ReadFile("../../../research/fixtures/group-metadata/profile-content.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Content   string
			Present   bool
			Image     *string
			FullImage *string
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, c := range fixture.Cases {
		got, err := ProjectGroupDisplay(ChatData{ChatMetas: []ChatMeta{{Type: SharedMetaProfile, Revision: 7, Content: c.Content}}})
		if err != nil {
			t.Fatal(err)
		}
		image, full := "", ""
		if c.Image != nil {
			image = *c.Image
		}
		if c.FullImage != nil {
			full = *c.FullImage
		}
		// A missing model/field becomes an empty projected URL, preserving the
		// observed display-clear boundary rather than exposing a JSON object.
		if got.ImageURL != image || got.FullImageURL != full || !got.AvatarKnown {
			t.Fatalf("content projection = %+v", got)
		}
	}
}

func TestDisplayContentRejectsAmbiguousInputs(t *testing.T) {
	for _, content := range []string{`{"imageUrl":"a","imageUrl":"b"}`, `{"imageUrl":7}`, `[]`, `{} {}`} {
		if _, err := ProjectGroupDisplay(ChatData{ChatMetas: []ChatMeta{{Type: SharedMetaProfile, Content: content}}}); err == nil {
			t.Fatalf("accepted ambiguous profile %q", content)
		}
	}
	if _, err := ProjectGroupDisplay(ChatData{ChatMetas: []ChatMeta{{Type: SharedMetaTitle, Revision: 7, Content: "one"}, {Type: SharedMetaTitle, Revision: 7, Content: "two"}}}); err == nil {
		t.Fatal("conflicting equal revisions accepted")
	}
}

func TestExecutedPersonalSnapshotDisplayFixture(t *testing.T) {
	raw, err := os.ReadFile("../../../research/fixtures/group-metadata/personal-display.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Input          struct{ Name string }
			SourceSnapshot RoomMeta
			Expected       RoomMeta
		}
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, c := range fixture.Cases {
		t.Run(c.Input.Name, func(t *testing.T) {
			got, err := ProjectGroupDisplay(ChatData{Type: "MultiChat", Meta: &c.SourceSnapshot})
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != c.Expected.Name || got.ImageURL != c.Expected.ImageURL || got.FullImageURL != c.Expected.FullImageURL || !got.AvatarKnown {
				t.Fatalf("snapshot projection = %+v", got)
			}
		})
	}
}
