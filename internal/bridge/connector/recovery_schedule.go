package connector

import (
	"time"
)

// The reconnect schedule is the official Mac client's recovery episode
// (research/bridge/CONNECT-TRAFFIC.md, finding #7, and RECOVERY-TRACE.md): the
// first attempt after a session loss runs after 0.1 s, a failed attempt of
// any kind retries after a fixed 10 s, and an episode without a successful
// login ends at a 600 s watchdog. The native client then logs out locally
// and waits for the user. The bridge stops retrying and waits for the
// operator's reconnect command or a process restart instead.
const (
	firstRecoveryDelay = 100 * time.Millisecond
	episodeRetryDelay  = 10 * time.Second
	episodeWatchdog    = 600 * time.Second
)

// recoverySchedule is the state of the current recovery episode. An episode
// starts with its first login attempt and ends with a completed login or
// with the watchdog.
type recoverySchedule struct {
	// episodeAt is when the episode's first attempt began; zero when no
	// episode is running.
	episodeAt time.Time
	// attempt counts the attempts that failed in this episode.
	attempt int
	// gaveUp is set when the watchdog ended an episode; only an explicit
	// Connect clears it.
	gaveUp bool
}

// begin marks the start of a login attempt at now. It reports whether the
// attempt starts a new episode, whose watchdog the caller arms.
func (s *recoverySchedule) begin(now time.Time) bool {
	if !s.episodeAt.IsZero() {
		return false
	}
	s.episodeAt = now
	s.attempt = 0
	return true
}

// active reports whether an episode is running.
func (s *recoverySchedule) active() bool { return !s.episodeAt.IsZero() }

// succeeded ends the episode after a completed login.
func (s *recoverySchedule) succeeded() {
	s.episodeAt = time.Time{}
	s.attempt = 0
}

// failed records a failed attempt at now. It returns the delay before the
// next attempt, or false when that attempt would run at or after the
// watchdog, which then ends the episode.
func (s *recoverySchedule) failed(now time.Time) (time.Duration, bool) {
	s.begin(now)
	s.attempt++
	if !now.Add(episodeRetryDelay).Before(s.episodeAt.Add(episodeWatchdog)) {
		return 0, false
	}
	return episodeRetryDelay, true
}

// watchdogDelay is how long after now the episode's watchdog fires.
func (s *recoverySchedule) watchdogDelay(now time.Time) time.Duration {
	return max(s.episodeAt.Add(episodeWatchdog).Sub(now), 0)
}
