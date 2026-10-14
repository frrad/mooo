package connector

import (
	"context"
	"sort"
	"time"

	"maunium.net/go/mautrix/bridgev2/database"
)

// refreshOneGroupProfiles scans one managed room and only enqueues optional
// enrichment. Membership is not polled; paused groups retry on their own.
// Its short context bounds local database work, never a source request. It does
// not pause membership or end a session when optional metadata is unavailable.
func (kc *KakaoClient) refreshOneGroupProfiles(c kakaoClient) (connectionFailed bool) {
	if !kc.groupGate.TryLock() {
		return false
	}
	defer kc.groupGate.Unlock()
	kc.mu.Lock()
	stopping := kc.stopping || kc.client != c
	kc.mu.Unlock()
	if stopping {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := kc.store.userPortals(ctx, kc.login.UserLogin)
	if err != nil {
		kc.log().Warn().Msg("Could not list groups for profile refresh")
		return false
	}
	var eligible []*database.Portal
	for _, row := range rows {
		p, err := kc.store.portalByKey(ctx, row.Portal)
		if err != nil {
			kc.log().Warn().Msg("Could not read group for profile refresh")
			continue
		}
		if p == nil || p.MXID == "" || p.Receiver != kc.login.ID || p.RoomType == database.RoomTypeDM {
			continue
		}
		meta, ok := p.Metadata.(*KakaoPortalMetadata)
		if !ok || meta == nil || meta.SourceRemoved || meta.MembershipPending {
			continue
		}
		knownRegularGroup := meta.GroupMembershipManaged
		if loginMeta, ok := kc.login.Metadata.(*UserLoginMetadata); ok && loginMeta != nil {
			if attempt, exists := loginMeta.GroupCreates[string(p.MXID)]; exists && attempt.Bound && makePortalKey(attempt.ChatID, kc.login.ID) == p.PortalKey {
				knownRegularGroup = true
			}
		}
		if !knownRegularGroup {
			continue
		}
		eligible = append(eligible, p)
	}
	if len(eligible) == 0 {
		return false
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].ID < eligible[j].ID })
	next := eligible[0]
	for _, p := range eligible {
		if string(p.ID) > kc.profileRefreshAfter {
			next = p
			break
		}
	}
	kc.profileRefreshAfter = string(next.ID)
	chatID, err := parseChatID(next.ID)
	if err != nil {
		return false
	}
	kc.enqueueMemberEnrichment(chatID, next.Metadata.(*KakaoPortalMetadata).MembershipRoster)
	kc.enqueueExistingRoomEnrichment(ctx, chatID, refreshPersonal, false)
	kc.enqueueExistingRoomEnrichment(ctx, chatID, refreshBoards, false)
	return false
}
