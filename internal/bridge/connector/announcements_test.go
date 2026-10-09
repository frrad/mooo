package connector

import (
	"context"
	"errors"
	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
	"path/filepath"
	"testing"
	"time"
)

func TestAnnouncementTopicRevisionsAndRemoval(t *testing.T) {
	p := &bridgev2.Portal{Portal: &database.Portal{Metadata: &KakaoPortalMetadata{}}}
	m := chatmeta.MoimMeta{Type: chatmeta.MoimMetaNotice, UpdateRevision: 43, Content: `{"type":"TEXT","notice":true,"content":"Synthetic announcement"}`}
	info, err := announcementTopicInfo(p, []chatmeta.MoimMeta{m})
	if err != nil || info.Topic == nil || *info.Topic != "Synthetic announcement" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	info.ExtraUpdates(context.Background(), p)
	old := m
	old.UpdateRevision = 42
	old.Content = `{}`
	info, err = announcementTopicInfo(p, []chatmeta.MoimMeta{old})
	if err != nil || info.Topic != nil {
		t.Fatal("stale revision changed topic")
	}
	// A failed Matrix send keeps TopicSet false. Same-revision replay still
	// returns the desired topic so the framework can retry its state event.
	info, err = announcementTopicInfo(p, []chatmeta.MoimMeta{m})
	if err != nil || info.Topic == nil || *info.Topic != "Synthetic announcement" {
		t.Fatal("replay cannot retry")
	}
	m.UpdateRevision = 44
	m.Content = `{}`
	info, err = announcementTopicInfo(p, []chatmeta.MoimMeta{m})
	if err != nil || info.Topic == nil || *info.Topic != "" {
		t.Fatal("removal did not clear topic")
	}
	info.ExtraUpdates(context.Background(), p)
	if p.Metadata.(*KakaoPortalMetadata).AnnouncementRevision != 44 {
		t.Fatal("revision not saved")
	}
	info, err = announcementTopicInfo(p, nil)
	if err != nil || info.Topic == nil || *info.Topic != "" {
		t.Fatal("empty authoritative snapshot")
	}
}
func TestAnnouncementRejectsUnsupportedAndConflictingContent(t *testing.T) {
	p := &bridgev2.Portal{Portal: &database.Portal{Metadata: &KakaoPortalMetadata{}}}
	m := chatmeta.MoimMeta{Type: 1, UpdateRevision: 43, Content: `{"notice":true,"type":"IMAGE","content":"private opaque data"}`}
	if _, err := announcementTopicInfo(p, []chatmeta.MoimMeta{m}); err == nil {
		t.Fatal("unsupported content accepted")
	}
	m.Content = `{"notice":true,"type":"TEXT","content":"one"}`
	other := m
	other.Content = `{"notice":true,"type":"TEXT","content":"two"}`
	if _, err := announcementTopicInfo(p, []chatmeta.MoimMeta{m, other}); err == nil {
		t.Fatal("conflicting revision accepted")
	}
}

type announcementIntent struct {
	frameworkPersistenceIntent
	topics []string
	fail   bool
}

func (i *announcementIntent) SendState(_ context.Context, _ id.RoomID, typ event.Type, _ string, content *event.Content, _ time.Time) (*mautrix.RespSendEvent, error) {
	if typ == event.StateTopic {
		i.topics = append(i.topics, content.Parsed.(*event.TopicEventContent).Topic)
		if i.fail {
			return nil, errors.New("synthetic topic failure")
		}
	}
	return &mautrix.RespSendEvent{EventID: "$state:test"}, nil
}
func (f *frameworkPersistenceNetworkConnector) GetName() bridgev2.BridgeName {
	return (&KakaoConnector{}).GetName()
}
func TestAnnouncementFrameworkPersistenceFailureRetryAndDedup(t *testing.T) {
	ctx := context.Background()
	raw, err := dbutil.NewWithDialect(filepath.Join(t.TempDir(), "bridge.db"), "sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.RawDB.Close() }()
	intent := &announcementIntent{fail: true}
	b, err := newFrameworkConversionBridge(ctx, raw, intent)
	if err != nil {
		t.Fatal(err)
	}
	p, err := b.GetPortalByKey(ctx, makePortalKey(testChatID, makeUserLoginID(testSelfID)))
	if err != nil {
		t.Fatal(err)
	}
	m := chatmeta.MoimMeta{Type: 1, UpdateRevision: 43, Content: `{"type":"TEXT","notice":true,"content":"Synthetic announcement"}`}
	apply := func(portal *bridgev2.Portal) {
		t.Helper()
		info, err := announcementTopicInfo(portal, []chatmeta.MoimMeta{m})
		if err != nil {
			t.Fatal(err)
		}
		portal.UpdateInfo(ctx, info, nil, intent, time.Time{})
	}
	apply(p)
	if p.TopicSet {
		t.Fatal("failed topic send marked successful")
	}
	stored, err := b.DB.Portal.GetByKey(ctx, p.PortalKey)
	if err != nil {
		t.Fatal(err)
	}
	if stored.TopicSet || stored.Metadata.(*KakaoPortalMetadata).AnnouncementRevision != 43 {
		t.Fatalf("persisted failed send: %+v", stored)
	}
	// Reload through a new bridge, using the real database metadata decoder.
	b2, err := newFrameworkConversionBridge(ctx, raw, intent)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := b2.GetPortalByKey(ctx, p.PortalKey)
	if err != nil {
		t.Fatal(err)
	}
	intent.fail = false
	apply(p2)
	if !p2.TopicSet || len(intent.topics) != 2 {
		t.Fatal("same revision did not retry")
	}
	apply(p2)
	if len(intent.topics) != 2 {
		t.Fatal("duplicate resent topic")
	}
	m.UpdateRevision = 44
	m.Content = `{}`
	apply(p2)
	stored, err = b2.DB.Portal.GetByKey(ctx, p.PortalKey)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Topic != "" || !stored.TopicSet || stored.Metadata.(*KakaoPortalMetadata).AnnouncementRevision != 44 {
		t.Fatalf("removal not persisted: topic=%q set=%v metadata=%+v", stored.Topic, stored.TopicSet, stored.Metadata)
	}
	m.UpdateRevision = 42
	m.Content = `{"type":"TEXT","notice":true,"content":"stale"}`
	apply(p2)
	if len(intent.topics) != 3 {
		t.Fatal("stale revision resent topic")
	}
}

func TestGetChatInfoAnnouncementSnapshotAndUnsupportedContent(t *testing.T) {
	fake := &fakeKakao{chatInfo: chatmeta.ChatInfoResponse{ChatData: chatmeta.ChatData{ChatID: testChatID, Type: "MultiChat", ChatMetas: []chatmeta.ChatMeta{{Type: 3, Content: "Synthetic group"}}}}, moimResponse: chatmeta.MoimResponse{ChatID: testChatID, Metas: []chatmeta.MoimMeta{{Type: 1, UpdateRevision: 43, Content: `{"type":"TEXT","notice":true,"content":"Synthetic announcement"}`}}}}
	kc, _ := newTestClient(t, func() (kakaoClient, error) { return fake, nil })
	kc.client = fake
	p := &bridgev2.Portal{Portal: &database.Portal{PortalKey: makePortalKey(testChatID, makeUserLoginID(testSelfID))}}
	info, err := kc.GetChatInfo(context.Background(), p)
	if err != nil || info.Topic == nil || *info.Topic != "Synthetic announcement" {
		t.Fatalf("snapshot info=%+v err=%v", info, err)
	}
	fake.moimResponse.Metas[0].Content = `{"type":"IMAGE","notice":true}`
	info, err = kc.GetChatInfo(context.Background(), p)
	if err != nil || info.Topic != nil || info.Name == nil || *info.Name != "Synthetic group" {
		t.Fatalf("unsupported announcement blocked group metadata: %+v %v", info, err)
	}
	fake.moimResponse.ChatID = testChatID + 1
	if _, err := kc.GetChatInfo(context.Background(), p); !errors.Is(err, errChatInfoMismatch) {
		t.Fatalf("mismatched snapshot accepted: %v", err)
	}
}
