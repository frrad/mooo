package connector

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"
)

func TestRegularGroupEmptyPersonalAvatarRequestsClear(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	backend.chatInfo.ChatData.Meta = &chatmeta.RoomMeta{}
	info, err := kc.chatInfoFromClient(ctx, p, backend, true)
	if err != nil {
		t.Fatal(err)
	}
	if info.Avatar == nil || !info.Avatar.Remove {
		t.Fatal("empty source avatar did not request removal of prior Matrix avatar")
	}
}

func TestRegularGroupUsesNewerLegacyDisplayMetadata(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	backend.chatInfo.ChatData.ChatMetas = []chatmeta.ChatMeta{
		{Type: chatmeta.SharedMetaKakaoGroup, Revision: 11, Content: `{"group_name":"Legacy","group_profile_thumbnail_url":"https://example.invalid/legacy-small.png","group_profile_url":"https://example.invalid/legacy-full.png"}`},
		{Type: chatmeta.SharedMetaTitle, Revision: 10, Content: "Shared"},
		{Type: chatmeta.SharedMetaProfile, Revision: 10, Content: `{"imageUrl":"https://example.invalid/shared-small.png","fullImageUrl":"https://example.invalid/shared-full.png"}`},
	}
	info, err := kc.chatInfoFromClient(ctx, p, backend, true)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name == nil || *info.Name != "Legacy" {
		t.Fatal("newer legacy name did not win over older shared title")
	}
	expected := avatarFromURL("https://example.invalid/legacy-small.png")
	if info.Avatar == nil || info.Avatar.ID != expected.ID {
		t.Fatal("newer legacy image did not win")
	}
}

func TestRegularGroupNewerSharedProfileClearsLegacyAvatar(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	backend.chatInfo.ChatData.ChatMetas = []chatmeta.ChatMeta{
		{Type: chatmeta.SharedMetaKakaoGroup, Revision: 9, Content: `{"group_name":"Legacy","group_profile_thumbnail_url":"https://example.invalid/legacy-small.png"}`},
		{Type: chatmeta.SharedMetaProfile, Revision: 10, Content: `{"imageUrl":"","fullImageUrl":""}`},
	}
	info, err := kc.chatInfoFromClient(ctx, p, backend, true)
	if err != nil {
		t.Fatal(err)
	}
	if info.Avatar == nil || !info.Avatar.Remove {
		t.Fatal("newer shared clear did not remove prior avatar")
	}
}

func TestOutboundGroupMetadataIsExplicitlyRejected(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	names, ok := any(kc).(bridgev2.RoomNameHandlingNetworkAPI)
	if !ok {
		t.Fatal("outbound room name has no explicit handling path")
	}
	avatars, ok := any(kc).(bridgev2.RoomAvatarHandlingNetworkAPI)
	if !ok {
		t.Fatal("outbound room avatar has no explicit handling path")
	}
	if changed, err := names.HandleMatrixRoomName(context.Background(), nil); changed || err == nil {
		t.Fatal("unsupported name change reported success")
	}
	if changed, err := avatars.HandleMatrixRoomAvatar(context.Background(), nil); changed || err == nil {
		t.Fatal("unsupported avatar change reported success")
	}
	if len(backend.sends) != 0 || backend.creates != 0 || backend.invites != 0 {
		t.Fatal("unsupported metadata mutated source")
	}
}

func TestRegularGroupOlderSnapshotCannotOverwriteNewerDisplay(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	backend.chatInfo.ChatData.ChatMetas = []chatmeta.ChatMeta{{Type: chatmeta.SharedMetaTitle, Revision: 9, Content: "Newer"}, {Type: chatmeta.SharedMetaProfile, Revision: 9, Content: `{"imageUrl":"","fullImageUrl":""}`}}
	if _, err = kc.chatInfoFromClient(ctx, p, backend, true); err != nil {
		t.Fatal(err)
	}
	// Reload the production portal and then supply an older source snapshot.
	p, err = kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	backend.chatInfo.ChatData.ChatMetas = []chatmeta.ChatMeta{{Type: chatmeta.SharedMetaTitle, Revision: 8, Content: "Older"}, {Type: chatmeta.SharedMetaProfile, Revision: 8, Content: `{"imageUrl":"https://example.invalid/old.png"}`}}
	info, err := kc.chatInfoFromClient(ctx, p, backend, true)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name == nil || *info.Name != "Newer" {
		t.Fatal("older snapshot overwrote newer title")
	}
	if info.Avatar == nil || !info.Avatar.Remove {
		t.Fatal("older snapshot resurrected cleared avatar")
	}
}

func TestDisplayCheckpointFailureCannotPublishNewerMetadata(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	backend.chatInfo.ChatData.ChatMetas = []chatmeta.ChatMeta{{Type: chatmeta.SharedMetaTitle, Revision: 9, Content: "Durable"}}
	if _, err = kc.chatInfoFromClient(ctx, p, backend, true); err != nil {
		t.Fatal(err)
	}
	db := kc.login.Bridge.DB.KV
	if _, err = db.Exec(ctx, `CREATE TRIGGER fail_display BEFORE INSERT ON kv_store BEGIN SELECT RAISE(ABORT,'synthetic checkpoint failure'); END`); err != nil {
		t.Fatal(err)
	}
	backend.chatInfo.ChatData.ChatMetas = []chatmeta.ChatMeta{{Type: chatmeta.SharedMetaTitle, Revision: 10, Content: "Unstored"}}
	if _, err = kc.chatInfoFromClient(ctx, p, backend, true); err == nil {
		t.Fatal("failed checkpoint reported display success")
	}
	if _, err = db.Exec(ctx, `DROP TRIGGER fail_display`); err != nil {
		t.Fatal(err)
	}
	// A new client has no volatile display cache; only the database can retain 9.
	reloaded := newKakaoClient(kc.login, kc.userID, nil)
	backend.chatInfo.ChatData.ChatMetas = []chatmeta.ChatMeta{{Type: chatmeta.SharedMetaTitle, Revision: 8, Content: "Older"}}
	info, err := reloaded.chatInfoFromClient(ctx, p, backend, true)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name == nil || *info.Name != "Durable" {
		t.Fatal("failed write damaged the prior durable display checkpoint")
	}
}

func TestDisplayCheckpointReadbackMismatchCannotReportSuccess(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = kc.login.Bridge.DB.KV.Exec(ctx, `CREATE TRIGGER corrupt_display AFTER INSERT ON kv_store BEGIN UPDATE kv_store SET value='corrupt' WHERE key=NEW.key AND bridge_id=NEW.bridge_id; END`); err != nil {
		t.Fatal(err)
	}
	if _, err = kc.chatInfoFromClient(ctx, p, backend, true); err == nil {
		t.Fatal("corrupted checkpoint was accepted")
	}
}

func TestDisplayCheckpointDoesNotRewritePortalAccessMetadata(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	p.Metadata = &KakaoPortalMetadata{SourceRemoved: true, MembershipPending: true, MembershipRoster: []int64{1000}, AnnouncementRevision: 17}
	if err = p.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = kc.chatInfoFromClient(ctx, p, backend, true); err != nil {
		t.Fatal(err)
	}
	saved, err := kc.login.Bridge.DB.Portal.GetByKey(ctx, p.PortalKey)
	if err != nil {
		t.Fatal(err)
	}
	meta := saved.Metadata.(*KakaoPortalMetadata)
	if !meta.SourceRemoved || !meta.MembershipPending || meta.AnnouncementRevision != 17 || len(meta.MembershipRoster) != 1 {
		t.Fatal("display checkpoint overwrote independent portal state")
	}
}

func TestPersonalMetadataNoticeRefreshesCurrentSnapshot(t *testing.T) {
	kc, backend, matrix := newGroupCreationFramework(t)
	ctx := context.Background()
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	p.MXID = "!selected:test"
	if err = p.Save(ctx); err != nil {
		t.Fatal(err)
	}
	backend.chatInfo.ChatData.Meta = &chatmeta.RoomMeta{Name: "Current personal name"}
	queued := 0
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		queued++
		//nolint:staticcheck // Drive the actual synchronous framework event consumer.
		return p.Internal().HandleRemoteEvent(ctx, kc.login, remote.GetType(), remote)
	}
	if !kc.handleEvent(backend, events.ChatMCMetaChanged{ChatID: 5000, Type: "name", Content: "Stale notice name", Revision: 1}) {
		t.Fatal("current snapshot refresh failed")
	}
	if queued != 1 || matrix.name != "Current personal name" {
		t.Fatal("personal notice did not project the fresh source snapshot")
	}
}

func TestDisplayCheckpointRejectsAmbiguousSnapshot(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	backend.chatInfo.ChatData.ChatMetas = []chatmeta.ChatMeta{
		{Type: chatmeta.SharedMetaTitle, Revision: 9, Content: "First"},
		{Type: chatmeta.SharedMetaTitle, Revision: 9, Content: "Conflicting"},
	}
	if _, err = kc.chatInfoFromClient(ctx, p, backend, true); err == nil {
		t.Fatal("ambiguous snapshot was checkpointed")
	}
}

func TestGroupMetadataFrameworkRetriesFailedNameAndAvatarClear(t *testing.T) {
	kc, backend, matrix := newGroupCreationFramework(t)
	ctx := context.Background()
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	p.MXID = "!selected:test"
	p.Name, p.NameSet = "Previous", true
	p.AvatarMXC, p.AvatarSet = "mxc://test/previous", true
	p.AvatarID = "previous"
	matrix.name, matrix.avatar = p.Name, p.AvatarMXC
	if err = p.Save(ctx); err != nil {
		t.Fatal(err)
	}
	backend.chatInfo.ChatData.Meta = &chatmeta.RoomMeta{Name: "Current"}
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		//nolint:staticcheck // Exercise the framework's actual metadata consumer.
		return p.Internal().HandleRemoteEvent(ctx, kc.login, remote.GetType(), remote)
	}
	matrix.failName, matrix.failAvatar = true, true
	kc.handleEvent(backend, events.ChatMCMetaChanged{ChatID: 5000, Type: "name"})
	if p.NameSet || p.AvatarSet || matrix.name != "Previous" || matrix.avatar != "mxc://test/previous" {
		t.Fatal("failed Matrix metadata writes were marked as applied")
	}
	matrix.failName, matrix.failAvatar = false, false
	kc.handleEvent(backend, events.ChatMCMetaChanged{ChatID: 5000, Type: "imagePath"})
	if !p.NameSet || !p.AvatarSet || matrix.name != "Current" || matrix.avatar != "" {
		t.Fatal("fresh resync did not retry failed name and avatar clear")
	}
}

func TestPersonalNoticeMissingSnapshotCannotOverwritePersonalName(t *testing.T) {
	kc, backend, matrix := newGroupCreationFramework(t)
	ctx := context.Background()
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	p.MXID = "!selected:test"
	p.Name, p.NameSet = "Previous personal", true
	matrix.name = p.Name
	if err = p.Save(ctx); err != nil {
		t.Fatal(err)
	}
	// Owned B observed CHGMCMETA name followed by CHATINFO with no personal m.
	// The shared title in this response is not proof that the personal name cleared.
	backend.chatInfo.ChatData.Meta = nil
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		//nolint:staticcheck // Exercise actual framework metadata consumption.
		return p.Internal().HandleRemoteEvent(ctx, kc.login, remote.GetType(), remote)
	}
	kc.handleEvent(backend, events.ChatMCMetaChanged{ChatID: 5000, Type: "name", Content: "New personal", Revision: 9})
	if matrix.name != "Previous personal" {
		t.Fatal("missing personal snapshot overwrote personal name with shared title")
	}
}

func TestPersonalMetadataReadConvergesWhenCHATINFOOmitsPersonalFields(t *testing.T) {
	kc, backend, matrix := newGroupCreationFramework(t)
	ctx := context.Background()
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	p.MXID = "!selected:test"
	if err = p.Save(ctx); err != nil {
		t.Fatal(err)
	}
	backend.chatInfo.ChatData.Meta = nil
	backend.personalMeta = &chatmeta.RoomMeta{Name: "Current personal name"}
	kc.queue = func(remote bridgev2.RemoteEvent) bridgev2.EventHandlingResult {
		//nolint:staticcheck // Drive actual source-to-Matrix consumer.
		return p.Internal().HandleRemoteEvent(ctx, kc.login, remote.GetType(), remote)
	}
	kc.handleEvent(backend, events.ChatMCMetaChanged{ChatID: 5000, Type: "name", Content: "Delayed old name", Revision: 1})
	if matrix.name != "Current personal name" {
		t.Fatal("separate personal read did not supply current display name")
	}
	backend.personalMeta = &chatmeta.RoomMeta{}
	kc.handleEvent(backend, events.ChatMCMetaChanged{ChatID: 5000, Type: "name", Content: "Current personal name", Revision: 9})
	if matrix.name != "Synthetic created group" {
		t.Fatal("confirmed personal clear did not reveal shared name")
	}
}

func TestOutboundGroupMetadataFrameworkDispatchRejectsChanges(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	user := kc.login.User
	_, err := user.NewLogin(ctx, &database.UserLogin{ID: makeUserLoginID(1001), Metadata: &UserLoginMetadata{Profile: "synthetic"}}, &bridgev2.NewLoginParams{LoadUserLogin: func(_ context.Context, login *bridgev2.UserLogin) error {
		kc.login = login
		login.Client = kc
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	p.MXID = "!selected:test"
	p.Name, p.NameSet = "Source name", true
	p.AvatarMXC, p.AvatarSet = "mxc://test/source", true
	if err = p.Save(ctx); err != nil {
		t.Fatal(err)
	}
	key := ""
	for _, evt := range []*event.Event{
		{Type: event.StateRoomName, StateKey: &key, Content: event.Content{Parsed: &event.RoomNameEventContent{Name: "Unsupported outbound name"}}},
		{Type: event.StateRoomAvatar, StateKey: &key, Content: event.Content{Parsed: &event.RoomAvatarEventContent{URL: "mxc://test/outbound"}}},
	} {
		//nolint:staticcheck // Verify actual framework dispatch, not only interface methods.
		result := p.Internal().HandleMatrixEvent(ctx, user, evt, false)
		if result.Success || !result.SendMSS || !errors.Is(result.Error, errOutboundRoomMetadata) {
			t.Fatalf("outbound metadata dispatch did not return explicit rejection: %+v", result)
		}
		status := bridgev2.WrapErrorInStatus(result.Error)
		if status.Status != event.MessageStatusFail || status.ErrorReason != event.MessageStatusUnsupported || !status.IsCertain || !status.SendNotice || !strings.Contains(status.Message, "native client") {
			t.Fatalf("unsupported metadata lacks a certain, non-retryable user notice: %+v", status)
		}
	}
	if p.Name != "Source name" || p.AvatarMXC != "mxc://test/source" || len(backend.sends) != 0 || backend.creates != 0 || backend.invites != 0 {
		t.Fatal("rejected metadata dispatch changed portal or source")
	}
}

func TestDisplayCheckpointOversizedWriteCannotPoisonDurableState(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	backend.chatInfo.ChatData.ChatMetas = []chatmeta.ChatMeta{{Type: chatmeta.SharedMetaTitle, Revision: 9, Content: "Durable"}}
	if _, err = kc.chatInfoFromClient(ctx, p, backend, true); err != nil {
		t.Fatal(err)
	}
	backend.chatInfo.ChatData.ChatMetas = []chatmeta.ChatMeta{{Type: chatmeta.SharedMetaTitle, Revision: 10, Content: strings.Repeat("x", 300<<10)}}
	if _, err = kc.chatInfoFromClient(ctx, p, backend, true); err == nil {
		t.Fatal("oversized display checkpoint was persisted")
	}
	reloaded := newKakaoClient(kc.login, kc.userID, nil)
	backend.chatInfo.ChatData.ChatMetas = []chatmeta.ChatMeta{{Type: chatmeta.SharedMetaTitle, Revision: 8, Content: "Older"}}
	info, err := reloaded.chatInfoFromClient(ctx, p, backend, true)
	if err != nil || info.Name == nil || *info.Name != "Durable" {
		t.Fatal("oversized update damaged the prior durable checkpoint")
	}
}

func TestRegularGroupPersonalFullSizeAvatarFallback(t *testing.T) {
	kc, backend, _ := newGroupCreationFramework(t)
	ctx := context.Background()
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	backend.chatInfo.ChatData.Meta = &chatmeta.RoomMeta{FullImageURL: "https://example.invalid/personal-full.png"}
	info, err := kc.chatInfoFromClient(ctx, p, backend, true)
	if err != nil {
		t.Fatal(err)
	}
	expected := avatarFromURL("https://example.invalid/personal-full.png")
	if info.Avatar == nil || info.Avatar.Remove || info.Avatar.ID != expected.ID {
		t.Fatal("known full-size personal image did not survive the thumbnail fallback")
	}
}

func TestGroupAvatarFrameworkRetainsPreviousMediaUntilRecovery(t *testing.T) {
	kc, backend, matrix := newGroupCreationFramework(t)
	ctx := context.Background()
	p, err := kc.login.Bridge.GetPortalByKey(ctx, makePortalKey(5000, kc.login.ID))
	if err != nil {
		t.Fatal(err)
	}
	p.MXID = "!selected:test"
	p.AvatarID = "previous"
	p.AvatarMXC = "mxc://test/previous"
	p.AvatarSet = true
	matrix.avatar = p.AvatarMXC
	if err = p.Save(ctx); err != nil {
		t.Fatal(err)
	}
	backend.chatInfo.ChatData.Meta = &chatmeta.RoomMeta{ImageURL: "https://example.invalid/current.png"}
	downloadFails := true
	remote := kc.remoteEventFor(events.ChatMCMetaChanged{ChatID: 5000, Type: "imagePath"}).(*simplevent.ChatResync)
	original := remote.GetChatInfoFunc
	remote.GetChatInfoFunc = func(ctx context.Context, p *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
		info, err := original(ctx, p)
		if err != nil {
			return nil, err
		}
		// Inject the media dependency after the real source projection. This tests
		// framework persistence/reupload handling, not HTTP download permissiveness.
		info.Avatar.Get = func(context.Context) ([]byte, error) {
			if downloadFails {
				return nil, errors.New("synthetic avatar download failure")
			}
			return []byte("synthetic replacement image bytes"), nil
		}
		return info, nil
	}
	apply := func() { t.Helper(); p.Internal().HandleRemoteEvent(ctx, kc.login, remote.GetType(), remote) } //nolint:staticcheck // Exercise actual framework consumer.
	apply()
	if p.AvatarSet || p.AvatarMXC != "mxc://test/previous" || matrix.avatar != "mxc://test/previous" || matrix.uploads != 0 {
		t.Fatal("failed download replaced previous media or marked avatar applied")
	}
	downloadFails = false
	matrix.failUpload = true
	apply()
	if p.AvatarSet || p.AvatarMXC != "mxc://test/previous" || matrix.avatar != "mxc://test/previous" || matrix.uploads != 1 {
		t.Fatal("failed upload replaced previous media or was skipped on retry")
	}
	matrix.failUpload = false
	apply()
	if !p.AvatarSet || p.AvatarMXC != "mxc://test/uploaded-avatar" || matrix.avatar != p.AvatarMXC || matrix.uploads != 2 {
		t.Fatal("same source avatar failed to recover after upload failure")
	}
	apply()
	if matrix.uploads != 2 {
		t.Fatal("applied avatar was uploaded again on replay")
	}
}
