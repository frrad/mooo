package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
	"github.com/frrad/mooo/internal/protocol/events"
	"maunium.net/go/mautrix/bridgev2"
)

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

func (kc *KakaoClient) personalRevisionKey() string {
	return fmt.Sprintf("kakao:personal-meta-revision:%d", kc.userID)
}

// appliedPersonalRevision is the last personal-metadata revision the bridge
// applied, from GETMCMETA or CHGMCMETA. Zero when none was applied.
func (kc *KakaoClient) appliedPersonalRevision(ctx context.Context) (int32, error) {
	raw, found, err := kc.store.get(ctx, kc.personalRevisionKey())
	if err != nil || !found {
		return 0, err
	}
	revision, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, errRoomMetadataCache
	}
	return int32(revision), nil
}

func (kc *KakaoClient) recordPersonalRevision(ctx context.Context, revision int32) error {
	applied, err := kc.appliedPersonalRevision(ctx)
	if err != nil || revision <= applied {
		return err
	}
	return kc.store.put(ctx, kc.personalRevisionKey(), strconv.FormatInt(int64(revision), 10))
}

// personalRevisionDemand follows the official client's login rule: GETMCMETA
// only when the login-list mcmRevision exceeds the applied revision.
func (kc *KakaoClient) personalRevisionDemand(ctx context.Context, c chatMetaAPI) {
	revision, ok, err := c.PersonalMetaRevision(ctx)
	if err != nil || !ok {
		return
	}
	applied, err := kc.appliedPersonalRevision(ctx)
	if err != nil {
		kc.log().Warn().Str("error_class", sourceErrorClass(err)).Msg("Personal metadata revision is unreadable; skipping its login read")
		return
	}
	if revision > applied {
		kc.metadataRefresh.offer(metadataRefreshTask{kind: refreshPersonal, personalRevision: revision})
	}
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

// applyPersonalPush stores a CHGMCMETA name or image into the room's personal
// checkpoint and records its revision. It reports false, storing nothing, when
// the room's personal metadata was never read. A push no newer than the
// applied revision is accepted without changes.
func (kc *KakaoClient) applyPersonalPush(ctx context.Context, evt events.ChatMCMetaChanged) (bool, error) {
	q := kc.metadataRefresh
	q.mu.Lock()
	defer q.mu.Unlock()
	cache, err := kc.readRoomMetadata(ctx, evt.ChatID)
	if err != nil || cache.PersonalAt.IsZero() {
		return false, err
	}
	// A delayed push no newer than the applied revision changes nothing.
	applied, err := kc.appliedPersonalRevision(ctx)
	if err != nil || evt.Revision <= applied {
		return err == nil, err
	}
	personal := chatmeta.RoomMeta{}
	if cache.Personal != nil {
		personal = *cache.Personal
	}
	switch evt.Type {
	case "name":
		personal.Name = evt.Content
	case "imagePath":
		personal.ImageURL, personal.FullImageURL = evt.ImageURL, evt.FullImageURL
	}
	cache.Personal = &personal
	// A source read begun before this push must not overwrite it.
	if task, ok := q.snapshots[evt.ChatID]; ok {
		q.version++
		task.version = q.version
		q.snapshots[evt.ChatID] = task
	}
	if err = kc.writeRoomMetadata(ctx, cache); err != nil {
		return false, err
	}
	return true, kc.recordPersonalRevision(ctx, evt.Revision)
}
