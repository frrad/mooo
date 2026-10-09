package connector

import (
	"context"
	"fmt"
	"time"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/simplevent"
)

// KakaoPortalMetadata checkpoints the Boards revision independently of chat
// names and message cursors. Topics are plaintext Matrix state, even in an
// encrypted room; this projection is an operator-selected mapping.
type KakaoPortalMetadata struct {
	AnnouncementRevision int64 `json:"announcement_revision,omitempty"`
}

func (kc *KakaoClient) announcementInfo(ctx context.Context, portal *bridgev2.Portal, c kakaoClient) (*bridgev2.ChatInfo, error) {
	chatID, err := parseChatID(portal.ID)
	if err != nil {
		return nil, err
	}
	r, err := c.MoimMeta(ctx, chatID)
	if err != nil {
		return nil, err
	}
	if r.ChatID != chatID {
		return nil, errChatInfoMismatch
	}
	return announcementTopicInfo(portal, r.Metas)
}

func announcementTopicInfo(portal *bridgev2.Portal, metas []chatmeta.MoimMeta) (*bridgev2.ChatInfo, error) {
	var selected *chatmeta.MoimMeta
	for i := range metas {
		m := &metas[i]
		if m.Type != chatmeta.MoimMetaNotice {
			continue
		}
		if selected != nil && m.UpdateRevision == selected.UpdateRevision && m.Content != selected.Content {
			return nil, fmt.Errorf("conflicting announcement revision")
		}
		if selected == nil || m.UpdateRevision > selected.UpdateRevision {
			selected = m
		}
	}
	metadata, ok := portal.Metadata.(*KakaoPortalMetadata)
	if !ok || metadata == nil {
		metadata = &KakaoPortalMetadata{}
	}
	revision := metadata.AnnouncementRevision
	topic := ""
	if selected != nil {
		if selected.UpdateRevision < revision {
			return &bridgev2.ChatInfo{}, nil
		}
		a, err := selected.Announcement()
		if err != nil {
			return nil, err
		}
		if a.Active {
			topic = a.Text
		}
		revision = selected.UpdateRevision
	}
	return &bridgev2.ChatInfo{Topic: &topic, ExtraUpdates: func(_ context.Context, p *bridgev2.Portal) bool {
		m, ok := p.Metadata.(*KakaoPortalMetadata)
		if !ok || m == nil {
			m = &KakaoPortalMetadata{}
			p.Metadata = m
		}
		if m.AnnouncementRevision == revision {
			return false
		}
		m.AnnouncementRevision = revision
		return true
	}}, nil
}

func (kc *KakaoClient) announcementResync(chatID int64) bridgev2.RemoteEvent {
	return &simplevent.ChatResync{EventMeta: simplevent.EventMeta{
		Type: bridgev2.RemoteEventChatResync, PortalKey: makePortalKey(chatID, kc.login.ID), Sender: kc.selfSender(),
	}, GetChatInfoFunc: func(ctx context.Context, p *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
		if p == nil {
			return nil, errChatInfoMismatch
		}
		id, err := parseChatID(p.ID)
		if err != nil || id != chatID {
			return nil, errChatInfoMismatch
		}
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		// The source snapshot, rather than a delayed push payload, selects the topic.
		return kc.GetChatInfo(ctx, p)
	}}
}

// Refresh existing group portals after reconnect, including removals made
// while offline. The event reads a fresh snapshot when the portal processes it.
func (kc *KakaoClient) refreshAnnouncements(ctx context.Context) error {
	if kc.login == nil || kc.login.Bridge == nil || kc.login.Bridge.DB == nil {
		return nil
	}
	rows, err := kc.login.Bridge.DB.UserPortal.GetAllForLogin(ctx, kc.login.UserLogin)
	if err != nil {
		return err
	}
	for _, row := range rows {
		p, err := kc.login.Bridge.DB.Portal.GetByKey(ctx, row.Portal)
		if err != nil {
			return err
		}
		if p == nil || p.MXID == "" || p.RoomType == database.RoomTypeDM {
			continue
		}
		id, err := parseChatID(p.ID)
		if err != nil {
			return err
		}
		if result := kc.queue(kc.announcementResync(id)); !committable(result) {
			return fmt.Errorf("queue announcement refresh")
		}
	}
	return nil
}
