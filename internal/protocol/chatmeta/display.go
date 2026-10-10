package chatmeta

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
		fields, err := displayContent(profile.Content, "imageUrl", "fullImageUrl")
		if err != nil {
			return GroupDisplay{}, err
		}
		out.ImageURL, out.FullImageURL = fields["imageUrl"], fields["fullImageUrl"]
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

// displayContent bounds input and rejects ambiguous duplicate keys. These are
// bridge admission rules, not claims about official malformed-input behavior.
func displayContent(content string, keys ...string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	if content == "" {
		return out, nil
	}
	if len(content) > 64<<10 {
		return nil, ErrInvalidResponse
	}
	decoder := json.NewDecoder(bytes.NewBufferString(content))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrInvalidResponse
	}
	known := map[string]bool{}
	for _, key := range keys {
		known[key] = true
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err = decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return nil, ErrInvalidResponse
		}
		seen[key] = true
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			return nil, ErrInvalidResponse
		}
		if known[key] {
			var value string
			if json.Unmarshal(raw, &value) != nil {
				return nil, ErrInvalidResponse
			}
			out[key] = value
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, ErrInvalidResponse
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, ErrInvalidResponse
	}
	return out, nil
}
