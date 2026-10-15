package connector

import (
	"context"
	"time"
)

// reconnectRecord is the login's persisted reconnect schedule. The framework
// loads a login into a fresh KakaoClient when the process starts, so without
// it a restart would forget a pending backoff, and a supervisor restarting a
// crashing bridge would log in to KakaoTalk on every restart.
type reconnectRecord struct {
	// Attempt counts the failed attempts of the current recovery episode.
	Attempt int `json:"attempt"`
	// LoginAt is when the latest login attempt began, in Unix milliseconds.
	LoginAt int64 `json:"login_at"`
	// NotBefore is when a scheduled retry may log in, in Unix milliseconds;
	// zero when no retry is pending.
	NotBefore int64 `json:"not_before,omitempty"`
	// EpisodeAt is when the current recovery episode's first attempt began,
	// in Unix milliseconds; zero when no episode is running.
	EpisodeAt int64 `json:"episode_at,omitempty"`
	// RateLimited counts the episode's LOGINLIST -328 replies.
	RateLimited int `json:"rate_limited,omitempty"`
	// ShortSessions counts the consecutive sessions that ended soon after
	// their login.
	ShortSessions int `json:"short_sessions,omitempty"`
	// Slow is set once the episode has left the episode cadence.
	Slow bool `json:"slow,omitempty"`
}

const loginSaveTimeout = 5 * time.Second

func newReconnectRecord(schedule recoverySchedule) reconnectRecord {
	record := reconnectRecord{Attempt: schedule.attempt, RateLimited: schedule.rateLimited, ShortSessions: schedule.shortSessions, Slow: schedule.slow}
	if !schedule.episodeAt.IsZero() {
		record.EpisodeAt = schedule.episodeAt.UnixMilli()
	}
	return record
}

func (r *reconnectRecord) schedule() recoverySchedule {
	schedule := recoverySchedule{attempt: r.Attempt, rateLimited: r.RateLimited, shortSessions: r.ShortSessions, slow: r.Slow}
	if r.EpisodeAt != 0 {
		schedule.episodeAt = time.UnixMilli(r.EpisodeAt)
	}
	return schedule
}

// restartSchedule returns how long a freshly loaded login waits before its
// first login, and the schedule that login continues.
//
// A scheduled retry continues its episode: a pending one is waited out, and
// one that came due while the process was down runs at once. Otherwise the
// previous process's last login began less than shortSessionDuration ago,
// for example before a crash, and the restart counts as a short session, as
// within one process. A supervisor that restarts a crashing bridge therefore
// joins the slow tier after maxShortSessions restarts instead of logging in
// on every one.
func restartSchedule(record *reconnectRecord, now time.Time) (time.Duration, recoverySchedule) {
	if record == nil {
		return 0, recoverySchedule{}
	}
	if record.NotBefore != 0 {
		// A clock that moved backwards must not stall the bridge for longer
		// than the slowest scheduled retry.
		wait := min(time.UnixMilli(record.NotBefore).Sub(now), slowRecoveryDelay)
		return max(wait, 0), record.schedule()
	}
	loginAt := time.UnixMilli(record.LoginAt)
	schedule := recoverySchedule{shortSessions: record.ShortSessions}
	delay, _ := schedule.sessionEnded(now.Sub(loginAt))
	if schedule.shortSessions == 0 {
		return 0, schedule
	}
	wait := min(loginAt.Add(delay).Sub(now), delay)
	return max(wait, 0), schedule
}

// restoredSchedule reads the persisted schedule on this client's first
// Connect only. Later Connects in the same process keep the in-memory
// schedule.
func (kc *KakaoClient) restoredSchedule() (wait time.Duration, schedule recoverySchedule, restored bool) {
	kc.metaMu.Lock()
	defer kc.metaMu.Unlock()
	if kc.scheduleRestored {
		return 0, recoverySchedule{}, false
	}
	kc.scheduleRestored = true
	meta, ok := kc.login.Metadata.(*UserLoginMetadata)
	if !ok {
		return 0, recoverySchedule{}, true
	}
	wait, schedule = restartSchedule(meta.Reconnect, kc.now())
	return wait, schedule, true
}

// persistedRetryPending reports whether the login's metadata holds a retry
// that may not log in yet.
func (kc *KakaoClient) persistedRetryPending() bool {
	kc.metaMu.Lock()
	defer kc.metaMu.Unlock()
	meta, ok := kc.login.Metadata.(*UserLoginMetadata)
	return ok && meta.Reconnect != nil && meta.Reconnect.NotBefore != 0 && time.UnixMilli(meta.Reconnect.NotBefore).After(kc.now())
}

// recordLoginAttempt persists the start of a login before it reaches the
// network.
func (kc *KakaoClient) recordLoginAttempt(schedule recoverySchedule, at time.Time) {
	kc.saveLoginMetadata("login attempt", func(meta *UserLoginMetadata) {
		record := newReconnectRecord(schedule)
		record.LoginAt = at.UnixMilli()
		meta.Reconnect = &record
	})
}

// recordScheduledRetry persists a pending retry and the schedule it belongs
// to.
func (kc *KakaoClient) recordScheduledRetry(schedule recoverySchedule, notBefore time.Time) {
	kc.saveLoginMetadata("scheduled reconnect", func(meta *UserLoginMetadata) {
		record := newReconnectRecord(schedule)
		if meta.Reconnect != nil {
			record.LoginAt = meta.Reconnect.LoginAt
		}
		record.NotBefore = notBefore.UnixMilli()
		meta.Reconnect = &record
	})
}

// saveLoginMetadata applies update to the login's metadata and saves it. A
// failed save is logged; the in-memory state still applies to this process.
func (kc *KakaoClient) saveLoginMetadata(what string, update func(*UserLoginMetadata)) {
	kc.metaMu.Lock()
	defer kc.metaMu.Unlock()
	meta, ok := kc.login.Metadata.(*UserLoginMetadata)
	if !ok {
		kc.log().Error().Str("record", what).Msg("Cannot save login state: login has no Kakao metadata")
		return
	}
	update(meta)
	ctx, cancel := context.WithTimeout(context.Background(), loginSaveTimeout)
	defer cancel()
	if err := kc.saveLogin(ctx); err != nil {
		kc.log().Err(err).Str("record", what).Msg("Failed to save login state; a restart would not see it")
	}
}
