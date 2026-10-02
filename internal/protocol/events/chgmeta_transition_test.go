package events

import "testing"

func TestReduceChatMetaRequiresRoomAndMergesBeforeSubtypeEffects(t *testing.T) {
	change := ChatMetaChanged{ChatID: 42, Type: 14, Revision: 9, Content: "synthetic opaque metadata"}

	missingRoom := ReduceChatMeta(ChatMetaState{}, change)
	if missingRoom.Applied || missingRoom.OpenLinkUpdated || missingRoom.CalendarSynced {
		t.Fatalf("missing-room transition = %#v, want no persistence or downstream effect", missingRoom)
	}

	storedRevision := int64(7)
	existingRoom := ReduceChatMeta(ChatMetaState{
		RoomExists: true, OpenChatBotEnabled: true, OpenLinkRevision: &storedRevision,
	}, change)
	if !existingRoom.Applied {
		t.Fatal("existing room did not apply the generic metadata merge")
	}
	if !existingRoom.OpenLinkUpdated {
		t.Fatal("strictly newer subtype-14 metadata did not trigger the open-link update")
	}
	if existingRoom.CalendarSynced {
		t.Fatal("subtype-14 metadata unexpectedly triggered calendar synchronization")
	}
}

func TestReduceChatMetaSuppressesOnlyStaleOpenLinkRefresh(t *testing.T) {
	for _, test := range []struct {
		name                             string
		storedRevision, incomingRevision int64
		wantOpenLink                     bool
	}{
		{name: "stale", storedRevision: 10, incomingRevision: 9, wantOpenLink: false},
		{name: "duplicate", storedRevision: 10, incomingRevision: 10, wantOpenLink: false},
		{name: "newer", storedRevision: 10, incomingRevision: 11, wantOpenLink: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := ChatMetaState{RoomExists: true, OpenChatBotEnabled: true, OpenLinkRevision: &test.storedRevision}
			result := ReduceChatMeta(state, ChatMetaChanged{ChatID: 42, Type: 14, Revision: test.incomingRevision})
			if !result.Applied || result.OpenLinkUpdated != test.wantOpenLink {
				t.Fatalf("transition = %#v, want applied and open-link=%v", result, test.wantOpenLink)
			}
		})
	}
}

func TestReduceChatMetaCalendarEffectKeepsNumericSubtypesOpaque(t *testing.T) {
	for _, subtype := range []int32{3, 15} {
		t.Run("teamchat", func(t *testing.T) {
			result := ReduceChatMeta(ChatMetaState{RoomExists: true, TeamChat: true}, ChatMetaChanged{ChatID: 42, Type: subtype})
			if !result.Applied || !result.CalendarSynced {
				t.Fatalf("subtype %d transition = %#v, want merge followed by calendar sync", subtype, result)
			}
		})
	}
	result := ReduceChatMeta(ChatMetaState{RoomExists: true}, ChatMetaChanged{ChatID: 42, Type: 3})
	if !result.Applied || result.CalendarSynced {
		t.Fatalf("non-teamchat transition = %#v, want merge without calendar sync", result)
	}
}

func TestReduceChatMetaLeavesUnknownSubtypeWithoutDownstreamEffect(t *testing.T) {
	result := ReduceChatMeta(ChatMetaState{RoomExists: true, TeamChat: true}, ChatMetaChanged{Type: 999})
	if !result.Applied || result.OpenLinkUpdated || result.CalendarSynced {
		t.Fatalf("unknown subtype transition = %#v, want only generic application", result)
	}
}

func TestReduceChatMetaRequiresEnabledOpenChatAndStoredRevision(t *testing.T) {
	for _, test := range []struct {
		name  string
		state ChatMetaState
	}{
		{name: "feature disabled", state: ChatMetaState{RoomExists: true}},
		{name: "stored revision absent", state: ChatMetaState{RoomExists: true, OpenChatBotEnabled: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := ReduceChatMeta(test.state, ChatMetaChanged{Type: 14, Revision: 9})
			if !result.Applied || result.OpenLinkUpdated {
				t.Fatalf("transition = %#v, want applied without open-link update", result)
			}
		})
	}
}
