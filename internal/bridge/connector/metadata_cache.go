package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"maunium.net/go/mautrix/bridgev2"
)

const roomMetadataFreshness = 15 * time.Minute

var errRoomMetadataCache = errors.New("connector: room enrichment checkpoint is invalid")

type roomMetadataCache struct {
	Version    int                 `json:"version"`
	AccountID  int64               `json:"account_id"`
	ChatID     int64               `json:"chat_id"`
	PersonalAt time.Time           `json:"personal_at,omitempty"`
	Personal   *chatmeta.RoomMeta  `json:"personal,omitempty"`
	BoardsAt   time.Time           `json:"boards_at,omitempty"`
	Boards     []chatmeta.MoimMeta `json:"boards,omitempty"`
}

func (kc *KakaoClient) roomMetadataKey(chatID int64) string {
	return fmt.Sprintf("kakao:room-enrichment:%d:%d", kc.userID, chatID)
}
func (kc *KakaoClient) readRoomMetadata(ctx context.Context, chatID int64) (roomMetadataCache, error) {
	raw, found, err := kc.store.get(ctx, kc.roomMetadataKey(chatID))
	out := roomMetadataCache{Version: 1, AccountID: kc.userID, ChatID: chatID}
	if err != nil || !found {
		return out, err
	}
	out = roomMetadataCache{}
	if len(raw) > 1<<20 || json.Unmarshal([]byte(raw), &out) != nil || out.Version != 1 || out.AccountID != kc.userID || out.ChatID != chatID {
		return out, errRoomMetadataCache
	}
	return out, nil
}
func (kc *KakaoClient) writeRoomMetadata(ctx context.Context, record roomMetadataCache) error {
	encoded, err := json.Marshal(record)
	if err != nil || len(encoded) > 1<<20 {
		return errRoomMetadataCache
	}
	return kc.store.put(ctx, kc.roomMetadataKey(record.ChatID), string(encoded))
}

func (kc *KakaoClient) cachedMemberProfiles(ctx context.Context, ids []int64) ([]chatmeta.Member, error) {
	var profiles []chatmeta.Member
	seen := map[int64]bool{}
	for _, userID := range ids {
		if userID <= 0 || userID == kc.userID || seen[userID] {
			continue
		}
		seen[userID] = true
		record, _, err := kc.readMemberProfile(ctx, userID)
		if err != nil {
			return profiles, err
		}
		if profile, ok := record.member(); ok {
			profiles = append(profiles, profile)
		}
	}
	return kc.cacheProfiles(profiles, ids), nil
}

// Existing rooms can publish their verified base snapshot immediately. Only
// authoritative membership reconciliation replaces a roster; ordinary metadata
// demand queues enrichment and carries no membership changes.
func (kc *KakaoClient) cachedChatInfo(ctx context.Context, p *bridgev2.Portal) (*bridgev2.ChatInfo, bool, error) {
	if p.MXID == "" {
		return nil, false, nil
	}
	chatID, err := parseChatID(p.ID)
	if err != nil {
		return nil, false, err
	}
	if meta, ok := p.Metadata.(*KakaoPortalMetadata); ok && meta != nil && (meta.MembershipPending || meta.SourceRemoved) {
		return nil, false, nil
	}
	q := kc.metadataRefresh
	q.mu.Lock()
	task, ok := q.snapshots[chatID]
	q.mu.Unlock()
	if !ok || len(task.snapshot.roster) == 0 {
		return nil, false, nil
	}
	cache, err := kc.readRoomMetadata(ctx, chatID)
	if err != nil {
		// Like a snapshot read, an unusable checkpoint loses only enrichment.
		kc.log().Warn().Str("error_class", sourceErrorClass(err)).Msg("Room enrichment is unavailable; preserving verified identities")
		cache = roomMetadataCache{Version: 1, AccountID: kc.userID, ChatID: chatID}
	}
	snap := task.snapshot
	if cache.Personal != nil {
		snap.data.Meta = cache.Personal
	}
	snap.moimMetas, snap.moimKnown = cache.Boards, !cache.BoardsAt.IsZero()
	snap.profiles, err = kc.cachedMemberProfiles(ctx, snap.profileIDs())
	if err != nil {
		return nil, true, err
	}
	if snap.data.Type == "MultiChat" {
		snap.data, err = kc.checkpointGroupDisplay(ctx, p, snap.data)
		if err != nil {
			return nil, true, err
		}
	}
	info, err := kc.projectChatInfo(p, snap)
	if err != nil {
		return nil, true, err
	}
	info.Members = nil
	task.kind = refreshRoomInfo
	q.offer(task)
	kc.enqueueExistingRoomEnrichment(ctx, chatID, refreshPersonal, false)
	kc.enqueueExistingRoomEnrichment(ctx, chatID, refreshBoards, false)
	return info, true, nil
}
