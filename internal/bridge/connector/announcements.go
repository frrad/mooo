package connector

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/simplevent"
)

// KakaoPortalMetadata checkpoints the Boards revision independently of chat
// names and message cursors. Topics are plaintext Matrix state, even in an
// encrypted room; this projection is an operator-selected mapping.
type KakaoPortalMetadata struct {
	AnnouncementRevision   int64   `json:"announcement_revision,omitempty"`
	SourceRemoved          bool    `json:"source_removed,omitempty"`
	MembershipPending      bool    `json:"membership_pending,omitempty"`
	MembershipRoster       []int64 `json:"membership_roster,omitempty"`
	GroupMembershipManaged bool    `json:"group_membership_managed,omitempty"`
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

// announcementResync applies a CHGMOMETAS payload. The official client merges
// the pushed metas into its stored Boards and never refetches for a push
// (research/bridge/METADATA-TRIGGERS.md). A room whose Boards were never read
// has nothing to merge into, so it reads them once instead.
func (kc *KakaoClient) announcementResync(evt events.ChatMoimMetaChanged) bridgev2.RemoteEvent {
	chatID := evt.ChatID
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
		merged, err := kc.mergeBoardsPush(ctx, chatID, evt.Metas)
		if err != nil {
			return nil, err
		}
		if merged {
			if kc.enqueueExistingRoomEnrichment(ctx, chatID, refreshBoards, false) {
				return &bridgev2.ChatInfo{}, nil
			}
			return kc.GetChatInfo(ctx, p)
		}
		if kc.enqueueExistingRoomEnrichment(ctx, chatID, refreshBoards, true) {
			return &bridgev2.ChatInfo{}, nil
		}
		kc.invalidateEnrichment(ctx, chatID, refreshBoards)
		return kc.GetChatInfo(ctx, p)
	}}
}

// mergeBoardsPush stores pushed metas into an existing Boards checkpoint. It
// reports false, storing nothing, when the room's Boards were never read.
func (kc *KakaoClient) mergeBoardsPush(ctx context.Context, chatID int64, pushed []chatmeta.MoimMeta) (bool, error) {
	q := kc.metadataRefresh
	q.mu.Lock()
	defer q.mu.Unlock()
	cache, err := kc.readRoomMetadata(ctx, chatID)
	if err != nil || cache.BoardsAt.IsZero() {
		return false, err
	}
	cache.Boards = mergeMoimMetas(cache.Boards, pushed)
	// A source read begun before this push must not overwrite it.
	if task, ok := q.snapshots[chatID]; ok {
		q.version++
		task.version = q.version
		q.snapshots[chatID] = task
	}
	return true, kc.writeRoomMetadata(ctx, cache)
}

// mergeMoimMetas applies pushed metas per type. A revision of zero or less
// deletes the type; otherwise only a newer revision replaces the stored entry,
// so a delayed push cannot roll Boards back. Types absent from the push keep
// their stored entries.
func mergeMoimMetas(stored, pushed []chatmeta.MoimMeta) []chatmeta.MoimMeta {
	out := append([]chatmeta.MoimMeta(nil), stored...)
	for _, m := range pushed {
		i := slices.IndexFunc(out, func(s chatmeta.MoimMeta) bool { return s.Type == m.Type })
		switch {
		case m.UpdateRevision <= 0:
			if i >= 0 {
				out = slices.Delete(out, i, i+1)
			}
		case i < 0:
			out = append(out, m)
		case m.UpdateRevision > out[i].UpdateRevision:
			out[i] = m
		}
	}
	return out
}

// boardsRevisionDemand rereads Boards when chat entry reports a notice revision
// that differs from the stored one, as the official client does on CHATONROOM.
// A room whose Boards were never read is left to its first read.
func (kc *KakaoClient) boardsRevisionDemand(ctx context.Context, chatID int64, revisions map[int32]int64) {
	current, ok := revisions[chatmeta.MoimMetaNotice]
	if !ok {
		return
	}
	cache, err := kc.readRoomMetadata(ctx, chatID)
	if err != nil || cache.BoardsAt.IsZero() {
		return
	}
	var stored int64
	for _, m := range cache.Boards {
		if m.Type == chatmeta.MoimMetaNotice {
			stored = m.UpdateRevision
		}
	}
	if current != stored {
		kc.invalidateEnrichment(ctx, chatID, refreshBoards)
	}
}
