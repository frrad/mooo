package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/frrad/mooo/internal/protocol/chatmeta"
)

// The official Mac client sends MEMBER only for users it has no record of, and
// never refetches a known user on a timer (research/bridge/METADATA-TRIGGERS.md).
// A stored profile is therefore never due again. The attempt cooldown is
// bridge failure backoff, so failed/missing attempts cannot storm after restart.
const memberProfileAttemptCooldown = 2 * time.Minute
const maxMemberProfileRecord = 64 << 10

var errProfileCheckpoint = errors.New("connector: member profile checkpoint is invalid")

// Persist only Matrix's enrichment fields, never MEMBER access permits. This
// record is not a membership roster and must never authorize a join.
type memberProfileFields struct {
	Nickname  string `json:"nickname"`
	Thumbnail string `json:"thumbnail"`
	FullImage string `json:"full_image"`
}
type memberProfileRecord struct {
	Version       int                  `json:"version"`
	AccountID     int64                `json:"account_id"`
	UserID        int64                `json:"user_id"`
	LastAttemptAt time.Time            `json:"last_attempt_at"`
	NextAttemptAt time.Time            `json:"next_attempt_at"`
	FetchedAt     time.Time            `json:"fetched_at,omitempty"`
	Profile       *memberProfileFields `json:"profile,omitempty"`
}

func memberProfileKey(accountID, userID int64) string {
	return fmt.Sprintf("kakao:member-profile:%d:%d", accountID, userID)
}

func (kc *KakaoClient) readMemberProfile(ctx context.Context, userID int64) (memberProfileRecord, string, error) {
	raw, found, err := kc.store.get(ctx, memberProfileKey(kc.userID, userID))
	record := memberProfileRecord{Version: 1, AccountID: kc.userID, UserID: userID}
	if err != nil || !found {
		return record, "", err
	}
	record = memberProfileRecord{}
	if len(raw) > maxMemberProfileRecord || json.Unmarshal([]byte(raw), &record) != nil || record.Version != 1 || record.AccountID != kc.userID || record.UserID != userID || record.LastAttemptAt.IsZero() || record.NextAttemptAt.Before(record.LastAttemptAt) || (record.Profile == nil) != record.FetchedAt.IsZero() {
		return memberProfileRecord{}, "", errProfileCheckpoint
	}
	// A pending or failed attempt waits exactly one cooldown. A stored profile's
	// next-attempt time is unused; records from the earlier freshness policy
	// carry a later one and remain valid.
	if record.Profile == nil && (!record.NextAttemptAt.Equal(record.LastAttemptAt.Add(memberProfileAttemptCooldown)) || record.NextAttemptAt.After(kc.now().Add(memberProfileAttemptCooldown))) {
		return memberProfileRecord{}, "", errProfileCheckpoint
	}
	if record.Profile != nil && record.NextAttemptAt.Before(record.FetchedAt) {
		return memberProfileRecord{}, "", errProfileCheckpoint
	}
	if record.Profile != nil && !validMemberProfileFields(*record.Profile) {
		return memberProfileRecord{}, "", errProfileCheckpoint
	}
	return record, raw, nil
}

func validMemberProfileFields(profile memberProfileFields) bool {
	return len(profile.Nickname) <= 8192 && len(profile.Thumbnail) <= 8192 && len(profile.FullImage) <= 8192
}

func (r memberProfileRecord) member() (chatmeta.Member, bool) {
	if r.Profile == nil {
		return chatmeta.Member{}, false
	}
	return chatmeta.Member{UserID: r.UserID, Nickname: r.Profile.Nickname, ProfileImageURL: r.Profile.Thumbnail, FullProfileImageURL: r.Profile.FullImage}, true
}

// resolveMemberProfiles is shared by ordinary room snapshots and membership
// enrichment. Durable compare-and-swap coalesces per-user attempts across rooms
// and connector instances. Concurrent callers return cached fields or unresolved
// identities; they do not wait on another caller's request deadline.
func (kc *KakaoClient) resolveMemberProfiles(ctx context.Context, c chatMetaAPI, chatID int64, userIDs, refetch []int64) ([]chatmeta.Member, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if chatID <= 0 || kc.userID <= 0 {
		return nil, chatmeta.ErrInvalidRequest
	}
	results := map[int64]chatmeta.Member{}
	seen := map[int64]bool{}
	var ids, due []int64
	reservations := map[int64]string{}
	records := map[int64]memberProfileRecord{}
	output := func() []chatmeta.Member {
		var out []chatmeta.Member
		for _, userID := range ids {
			if profile, ok := results[userID]; ok {
				out = append(out, profile)
			}
		}
		return out
	}
	now := kc.now()
	for _, userID := range userIDs {
		if userID <= 0 || userID == kc.userID || seen[userID] {
			continue
		}
		seen[userID] = true
		ids = append(ids, userID)
		record, raw, err := kc.readMemberProfile(ctx, userID)
		if err != nil {
			return output(), err
		}
		if profile, ok := record.member(); ok {
			results[userID] = profile
		}
		// An empty stored nickname is refetched, as native message conversion
		// does, and so is a requested refetch, at most once per attempt cooldown.
		if record.Profile != nil && record.Profile.Nickname != "" && !slices.Contains(refetch, userID) || now.Before(record.NextAttemptAt) {
			continue
		}
		record.LastAttemptAt = now
		record.NextAttemptAt = now.Add(memberProfileAttemptCooldown)
		encoded, err := json.Marshal(record)
		if err != nil || len(encoded) > maxMemberProfileRecord {
			return output(), errProfileCheckpoint
		}
		changed, err := kc.store.compareAndSwap(ctx, memberProfileKey(kc.userID, userID), raw, string(encoded))
		if err != nil {
			return output(), err
		}
		if !changed {
			record, _, err = kc.readMemberProfile(ctx, userID)
			if err != nil {
				return output(), err
			}
			if profile, ok := record.member(); ok {
				results[userID] = profile
			}
			continue
		}
		reservations[userID] = string(encoded)
		records[userID] = record
		due = append(due, userID)
	}
	if len(due) == 0 {
		return output(), nil
	}
	// The real Client retains physical 500-ID batching and global admission.
	fetched, fetchErr := c.Members(ctx, chatID, due)
	// Bound publication of the entire partial result, independently of a caller
	// deadline that may have expired during a later physical batch.
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	returned := map[int64]bool{}
	for _, profile := range fetched {
		reservation, requested := reservations[profile.UserID]
		if !requested {
			continue
		}
		if returned[profile.UserID] {
			return output(), chatmeta.ErrInvalidResponse
		}
		returned[profile.UserID] = true
		fields := memberProfileFields{Nickname: profile.Nickname, Thumbnail: profile.ProfileImageURL, FullImage: profile.FullProfileImageURL}
		if !validMemberProfileFields(fields) {
			return output(), errProfileCheckpoint
		}
		record := records[profile.UserID]
		record.Profile = &fields
		record.FetchedAt = kc.now()
		record.NextAttemptAt = record.FetchedAt
		if fields.Nickname == "" {
			record.NextAttemptAt = record.FetchedAt.Add(memberProfileAttemptCooldown)
		}
		encoded, err := json.Marshal(record)
		if err != nil || len(encoded) > maxMemberProfileRecord {
			return output(), errProfileCheckpoint
		}
		changed, err := kc.store.compareAndSwap(persistCtx, memberProfileKey(kc.userID, profile.UserID), reservation, string(encoded))
		if err == nil && !changed {
			record, _, err = kc.readMemberProfile(persistCtx, profile.UserID)
		}
		if err != nil {
			return output(), err
		}
		if selected, ok := record.member(); ok {
			results[profile.UserID] = selected
		}
	}
	return output(), fetchErr
}
