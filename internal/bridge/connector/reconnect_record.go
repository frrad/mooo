package connector

import (
	"context"
	"time"

	"github.com/frrad/mooo/internal/client"
)

// reconnectRecord is the login's persisted reconnect schedule. The framework
// loads a login into a fresh KakaoClient when the process starts, so without
// it a restart would forget a pending backoff, and a supervisor restarting a
// crashing bridge would log in to KakaoTalk on every restart.
type reconnectRecord struct {
	// Attempt is the recovery attempt of the latest login or scheduled
	// retry, counted since the last stable session.
	Attempt int `json:"attempt"`
	// LoginAt is when the latest login attempt began, in Unix milliseconds.
	LoginAt int64 `json:"login_at"`
	// NotBefore is when a scheduled retry may log in, in Unix milliseconds;
	// zero when no retry is pending.
	NotBefore int64 `json:"not_before,omitempty"`
}

const loginSaveTimeout = 5 * time.Second

// restartSchedule returns how long a freshly loaded login waits before its
// first login, and the recovery attempt that login continues. A pending
// retry is waited out. A previous process that ended within a stable session
// of logging in, for example by crashing, counts as a short session: the
// schedule advances as it does for a short session within one process.
func restartSchedule(record *reconnectRecord, now time.Time) (time.Duration, int) {
	if record == nil {
		return 0, 0
	}
	if record.NotBefore != 0 {
		if wait := time.UnixMilli(record.NotBefore).Sub(now); wait > 0 {
			// A clock that moved backwards must not stall the bridge for
			// longer than the slowest scheduled retry.
			return min(wait, slowRecoveryDelay), record.Attempt
		}
	}
	loginAt := time.UnixMilli(record.LoginAt)
	if now.Sub(loginAt) >= stableSessionDuration {
		return 0, 0
	}
	delay := recoveryDelay(client.ErrClosed, record.Attempt)
	wait := min(loginAt.Add(delay).Sub(now), delay)
	return max(wait, 0), record.Attempt + 1
}

// restoredSchedule reads the persisted schedule on this client's first
// Connect only. Later Connects in the same process keep the in-memory
// schedule.
func (kc *KakaoClient) restoredSchedule() (wait time.Duration, attempt int, restored bool) {
	kc.metaMu.Lock()
	defer kc.metaMu.Unlock()
	if kc.scheduleRestored {
		return 0, 0, false
	}
	kc.scheduleRestored = true
	meta, ok := kc.login.Metadata.(*UserLoginMetadata)
	if !ok {
		return 0, 0, true
	}
	wait, attempt = restartSchedule(meta.Reconnect, kc.now())
	return wait, attempt, true
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
func (kc *KakaoClient) recordLoginAttempt(attempt int, at time.Time) {
	kc.saveLoginMetadata("login attempt", func(meta *UserLoginMetadata) {
		meta.Reconnect = &reconnectRecord{Attempt: attempt, LoginAt: at.UnixMilli()}
	})
}

// recordScheduledRetry persists a pending retry and the attempt it belongs
// to.
func (kc *KakaoClient) recordScheduledRetry(attempt int, notBefore time.Time) {
	kc.saveLoginMetadata("scheduled reconnect", func(meta *UserLoginMetadata) {
		next := reconnectRecord{}
		if meta.Reconnect != nil {
			next = *meta.Reconnect
		}
		next.Attempt = attempt
		next.NotBefore = notBefore.UnixMilli()
		meta.Reconnect = &next
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
