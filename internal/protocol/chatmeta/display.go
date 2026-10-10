package chatmeta

import (
	"encoding/json"
	"fmt"

	"github.com/frrad/mooo/internal/protocol/strictjson"
)

// GroupDisplay is the regular room's display projection before member-name
// fallback. Unknown avatar data must not clear an existing Matrix avatar.
type GroupDisplay struct {
	Name         string
	ImageURL     string
	FullImageURL string
	AvatarKnown  bool
}

// ProjectGroupDisplay applies the executed Mac personal/shared precedence.
// It does not implement the persistent per-type revision merge.
func ProjectGroupDisplay(data ChatData) (GroupDisplay, error) {
	out := GroupDisplay{AvatarKnown: data.Meta != nil}
	selected := map[int32]ChatMeta{}
	for _, meta := range data.ChatMetas {
		if meta.Type != SharedMetaKakaoGroup && meta.Type != SharedMetaTitle && meta.Type != SharedMetaProfile {
			continue
		}
		if prev, ok := selected[meta.Type]; ok {
			if meta.Revision < prev.Revision {
				continue
			}
			if meta.Revision == prev.Revision && meta.Content != prev.Content {
				return GroupDisplay{}, fmt.Errorf("%w: conflicting display revision", ErrInvalidResponse)
			}
		}
		selected[meta.Type] = meta
	}
	legacy, hasLegacy := selected[SharedMetaKakaoGroup]
	title, hasTitle := selected[SharedMetaTitle]
	profile, hasProfile := selected[SharedMetaProfile]
	var legacyFields map[string]string
	var err error
	if hasLegacy {
		legacyFields, err = displayContent(legacy.Content, "group_name", "group_profile_thumbnail_url", "group_profile_url")
		if err != nil {
			return GroupDisplay{}, err
		}
	}
	if hasLegacy && (!hasTitle || legacy.Revision > title.Revision) {
		out.Name = legacyFields["group_name"]
	} else if hasTitle {
		out.Name = title.Content
	}
	if hasLegacy && (!hasProfile || legacy.Revision > profile.Revision) {
		out.ImageURL, out.FullImageURL = legacyFields["group_profile_thumbnail_url"], legacyFields["group_profile_url"]
		out.AvatarKnown = true
	} else if hasProfile {
		urls, err := displayContent(profile.Content, "imageUrl", "fullImageUrl")
		if err != nil {
			return GroupDisplay{}, err
		}
		out.ImageURL, out.FullImageURL = urls["imageUrl"], urls["fullImageUrl"]
		out.AvatarKnown = true
	}
	if data.Meta != nil {
		if data.Meta.Name != "" {
			out.Name = data.Meta.Name
		}
		if data.Meta.ImageURL != "" {
			out.ImageURL = data.Meta.ImageURL
		}
		if data.Meta.FullImageURL != "" {
			out.FullImageURL = data.Meta.FullImageURL
		}
	}
	return out, nil
}

// displayJSON bounds the content size but not its key count.
var displayJSON = strictjson.Limits{MaxBytes: 64 << 10}

// displayContent bounds input and rejects ambiguous duplicate keys. These are
// bridge admission rules, not claims about official malformed-input behavior.
func displayContent(content string, keys ...string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	if content == "" {
		return out, nil
	}
	var fields map[string]json.RawMessage
	if strictjson.Unmarshal([]byte(content), &fields, displayJSON) != nil {
		return nil, ErrInvalidResponse
	}
	for _, key := range keys {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return nil, ErrInvalidResponse
		}
		out[key] = value
	}
	return out, nil
}
