package events

import "testing"

func TestReduceChatMCMetaRoutesFieldsAndAdvancesNewerRevision(t *testing.T) {
	state := ChatMCMetaState{
		RoomExists:     true,
		GlobalRevision: 7,
		Name:           "old",
		ImageURL:       "old-image",
		FullImageURL:   "old-full",
		Category:       "old-category",
		Pin:            3,
	}
	change := ChatMCMetaChanged{
		ChatID:       42,
		Revision:     9,
		Type:         "imagePath",
		ImageURL:     "new-image",
		FullImageURL: "new-full",
	}

	result := ReduceChatMCMeta(state, change)
	if !result.Applied || result.State.GlobalRevision != 9 {
		t.Fatalf("transition = %#v, want applied revision 9", result)
	}
	if result.State.ImageURL != "new-image" || result.State.FullImageURL != "new-full" {
		t.Fatalf("image state = %#v, want both URLs replaced", result.State)
	}
}

func TestReduceChatMCMetaRoutesStaleNoticeButDoesNotRegressRevision(t *testing.T) {
	state := ChatMCMetaState{
		RoomExists:     true,
		GlobalRevision: 12,
		Name:           "current",
	}
	result := ReduceChatMCMeta(state, ChatMCMetaChanged{
		ChatID:   42,
		Revision: 8,
		Type:     "name",
		Content:  "stale-field-update",
	})
	if !result.Applied {
		t.Fatal("known route must report a field mutation")
	}
	if result.State.GlobalRevision != 12 {
		t.Fatalf("global revision = %d, want unchanged 12", result.State.GlobalRevision)
	}
	if result.State.Name != "stale-field-update" {
		t.Fatalf("name = %q, want routed stale content", result.State.Name)
	}
}

func TestReduceChatMCMetaHiddenStateEmitsCleanupEffects(t *testing.T) {
	state := ChatMCMetaState{RoomExists: true, GlobalRevision: 2, Name: "room", Pin: 3}
	result := ReduceChatMCMeta(state, ChatMCMetaChanged{
		ChatID:   42,
		Revision: 3,
		Type:     "chat_hide",
		Content:  "true",
	})
	if !result.State.Hidden || result.State.Pin != -1 || !result.Unpin || !result.UnpinInAllFolders {
		t.Fatalf("hidden transition = %#v, want hidden plus cleanup effects", result)
	}
}

func TestReduceChatMCMetaCategoryAndImageCanClearValues(t *testing.T) {
	state := ChatMCMetaState{RoomExists: true, Category: "old", ImageURL: "old", FullImageURL: "old-full"}
	category := ReduceChatMCMeta(state, ChatMCMetaChanged{Type: "chat_category", Content: "new"})
	if category.State.Category != "new" {
		t.Fatalf("category = %q, want new", category.State.Category)
	}
	image := ReduceChatMCMeta(category.State, ChatMCMetaChanged{Type: "imagePath"})
	if image.State.ImageURL != "" || image.State.FullImageURL != "" {
		t.Fatalf("image URLs = %q/%q, want both cleared", image.State.ImageURL, image.State.FullImageURL)
	}
}

func TestReduceChatMCMetaEqualRevisionStillAppliesKnownRoute(t *testing.T) {
	state := ChatMCMetaState{RoomExists: true, GlobalRevision: 9, Name: "old"}
	result := ReduceChatMCMeta(state, ChatMCMetaChanged{Revision: 9, Type: "name", Content: "new"})
	if !result.Applied || result.State.GlobalRevision != 9 || result.State.Name != "new" {
		t.Fatalf("equal-revision transition = %#v, want routed field with revision 9", result)
	}
}

func TestReduceChatMCMetaDuplicateKnownRouteReportsAppliedWithoutStateChange(t *testing.T) {
	state := ChatMCMetaState{RoomExists: true, GlobalRevision: 9, Name: "same", Pin: 8}
	result := ReduceChatMCMeta(state, ChatMCMetaChanged{Revision: 9, Type: "name", Content: "same"})
	if !result.Applied || result.State != state || result.Unpin || result.UnpinInAllFolders {
		t.Fatalf("duplicate transition = %#v, want applied assignment with unchanged state", result)
	}
}

func TestReduceChatMCMetaEmptyImageFieldsReplaceExistingValues(t *testing.T) {
	state := ChatMCMetaState{RoomExists: true, ImageURL: "old", FullImageURL: "old-full"}
	result := ReduceChatMCMeta(state, ChatMCMetaChanged{Type: "imagePath"})
	if !result.Applied || result.State.ImageURL != "" || result.State.FullImageURL != "" {
		t.Fatalf("empty-image transition = %#v, want both URLs cleared", result)
	}
}

func TestReduceChatMCMetaBooleanLabelsUseExactTrue(t *testing.T) {
	for _, test := range []struct {
		typ     string
		content string
		want    bool
	}{
		{typ: "favorite", content: "true", want: true},
		{typ: "favorite", content: "True", want: false},
		{typ: "chat_hide", content: "false", want: false},
		{typ: "chat_hide", content: "1", want: false},
	} {
		t.Run(test.typ+"/"+test.content, func(t *testing.T) {
			result := ReduceChatMCMeta(ChatMCMetaState{RoomExists: true, Favorite: true, Hidden: true}, ChatMCMetaChanged{
				Type: test.typ, Content: test.content,
			})
			if result.State.Favorite != test.want && test.typ == "favorite" {
				t.Fatalf("favorite = %v, want %v", result.State.Favorite, test.want)
			}
			if result.State.Hidden != test.want && test.typ == "chat_hide" {
				t.Fatalf("hidden = %v, want %v", result.State.Hidden, test.want)
			}
		})
	}
}

func TestReduceChatMCMetaMissingRoomAndUnknownRouteAreNoOps(t *testing.T) {
	for _, test := range []struct {
		name   string
		state  ChatMCMetaState
		change ChatMCMetaChanged
	}{
		{
			name:   "missing room",
			state:  ChatMCMetaState{GlobalRevision: 4, Name: "keep"},
			change: ChatMCMetaChanged{Revision: 9, Type: "name", Content: "drop"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := ReduceChatMCMeta(test.state, test.change)
			if result.Applied || result.State != test.state {
				t.Fatalf("transition = %#v, want unchanged no-op", result)
			}
		})
	}
}

func TestReduceChatMCMetaUnknownTypeAdvancesOnlyNewerGlobalRevision(t *testing.T) {
	state := ChatMCMetaState{RoomExists: true, GlobalRevision: 4, Name: "keep"}
	result := ReduceChatMCMeta(state, ChatMCMetaChanged{Revision: 5, Type: "unproven", Content: "drop"})
	if !result.Applied || result.State.GlobalRevision != 5 || result.State.Name != "keep" {
		t.Fatalf("transition = %#v, want revision-only application", result)
	}
}

func TestReduceChatMCMetaUnknownNonNewerVisibleRoomIsNoOp(t *testing.T) {
	state := ChatMCMetaState{RoomExists: true, GlobalRevision: 4, Name: "keep", Pin: 8}
	result := ReduceChatMCMeta(state, ChatMCMetaChanged{Revision: 3, Type: "unproven", Content: "drop"})
	if result.Applied || result.Unpin || result.UnpinInAllFolders || result.State != state {
		t.Fatalf("unknown stale visible transition = %#v, want unchanged no-op", result)
	}
}

func TestReduceChatMCMetaPreexistingHiddenRoomStillUnpinsOnNonNewerUnknown(t *testing.T) {
	state := ChatMCMetaState{RoomExists: true, GlobalRevision: 4, Hidden: true, Pin: 8}
	result := ReduceChatMCMeta(state, ChatMCMetaChanged{Revision: 3, Type: "unproven"})
	if !result.Applied || result.State.GlobalRevision != 4 || result.State.Pin != -1 ||
		!result.Unpin || !result.UnpinInAllFolders {
		t.Fatalf("transition = %#v, want unchanged revision plus hidden cleanup", result)
	}
}

func TestReduceChatMCMetaMissingRoomNeverEmitsHiddenCleanup(t *testing.T) {
	state := ChatMCMetaState{Hidden: true, Pin: 8}
	result := ReduceChatMCMeta(state, ChatMCMetaChanged{Revision: 9, Type: "chat_hide", Content: "true"})
	if result.Applied || result.Unpin || result.UnpinInAllFolders || result.State != state {
		t.Fatalf("transition = %#v, want unchanged missing-room state without effects", result)
	}
}
