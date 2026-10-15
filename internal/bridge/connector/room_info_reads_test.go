package connector

import (
	"slices"
	"testing"

	"maunium.net/go/mautrix/bridgev2/simplevent"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
)

// A framework GetChatInfo for a room the bridge already projected reuses the
// cached snapshot. The official client sends CHATINFO only on its traced
// triggers, never on a cached room read, so nothing is queued.
func TestCachedGetChatInfoQueuesNoChatInfo(t *testing.T) {
	kc, backend, _ := newManagedFeedGroup(t)
	p, err := kc.login.Bridge.GetPortalByKey(t.Context(), makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = kc.GetChatInfo(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	drainMetadataRefresh(t, kc, backend)
	if calls := sourceCalls(backend); len(calls) != 0 {
		t.Fatalf("cached room read sent source requests: %v", calls)
	}
}

// CHGCHATST and CHGMETA carry payloads the bridge does not decode, so their
// resync still refreshes room info with one CHATINFO.
func TestChatMetaChangeResyncRefreshesRoomInfo(t *testing.T) {
	for _, notice := range []events.Event{events.ChatMetaChanged{ChatID: 5000}, events.ChatStatusChanged{ChatID: 5000}} {
		kc, backend, _ := newManagedFeedGroup(t)
		p, err := kc.login.Bridge.GetPortalByKey(t.Context(), makePortalKey(5000, kc.login.ID))
		if err != nil {
			t.Fatal(err)
		}
		resync := kc.remoteEventFor(notice).(*simplevent.ChatResync)
		if _, err = resync.GetChatInfoFunc(t.Context(), p); err != nil {
			t.Fatal(err)
		}
		drainMetadataRefresh(t, kc, backend)
		if calls := sourceCalls(backend); !slices.Equal(calls, []string{"ChatInfo(5000)"}) {
			t.Fatalf("%T resync source requests = %v, want one CHATINFO", notice, calls)
		}
	}
}

// A room avatar whose ID (the URL hash) equals the portal's stored avatar is
// already in Matrix; refreshing room info must not download it again.
func TestRoomInfoRefreshReusesUnchangedRoomAvatar(t *testing.T) {
	kc, backend, _ := newManagedFeedGroup(t)
	const imageURL = "https://example.invalid/unchanged-room.png"
	p, err := kc.login.Bridge.GetPortalByKey(t.Context(), makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	p.AvatarID, p.AvatarMXC, p.AvatarSet = avatarFromURL(imageURL).ID, "mxc://test/stored-room", true
	if err = p.Save(t.Context()); err != nil {
		t.Fatal(err)
	}
	backend.chatInfo.ChatData.Meta = &chatmeta.RoomMeta{ImageURL: imageURL}
	kc.metadataRefresh.mu.Lock()
	task := kc.metadataRefresh.snapshots[5000]
	kc.metadataRefresh.mu.Unlock()
	task.kind = refreshRoomInfo
	result := kc.performMetadataRefresh(t.Context(), backend, task)
	if result.err != nil {
		t.Fatalf("refresh tried to fetch the unchanged avatar: %v", result.err)
	}
	if result.info == nil || result.info.Avatar == nil || result.info.Avatar.MXC != "mxc://test/stored-room" || result.info.Avatar.Get != nil {
		t.Fatalf("refreshed avatar = %+v, want the stored media", result.info)
	}
}
