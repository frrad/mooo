package connector

import (
	"errors"
	"time"

	"github.com/frrad/mooo/internal/client"
)

// The reconnect schedule follows the official Mac client's recovery episode
// (research/bridge/CONNECT-TRAFFIC.md, finding #7): the first attempt after a
// session loss runs at once, a failed attempt retries after a fixed delay, and
// an episode without a successful login ends at a watchdog. The native client
// logs out at the watchdog; an unattended bridge instead reports a stalled
// state and keeps retrying every slowRecoveryDelay.
const (
	firstRecoveryDelay   = 100 * time.Millisecond
	episodeRetryDelay    = 10 * time.Second
	episodeWatchdog      = 600 * time.Second
	slowRecoveryDelay    = 15 * time.Minute
	shortSessionDuration = 60 * time.Second
	// maxShortSessions is how many sessions in a row may end within
	// shortSessionDuration of their login before reconnects join the slow
	// tier. The native client has no such guard; it stops CHANGESVR or
	// close-after-login loops from logging in every few seconds forever.
	maxShortSessions = 3
)

// rateLimitedRecoveryDelays is the bridge's own backoff for consecutive
// LOGINLIST -328 replies in one episode. Retrying a temporary device limit at
// the episode cadence would only repeat the refusal.
var rateLimitedRecoveryDelays = [...]time.Duration{60 * time.Second, 120 * time.Second, 240 * time.Second, 480 * time.Second, 900 * time.Second}

// recoverySchedule is the reconnect schedule state. An episode starts with
// its first login attempt and ends with a successful login.
type recoverySchedule struct {
	// episodeAt is when the episode's first attempt began; zero when no
	// episode is running.
	episodeAt time.Time
	// attempt counts the attempts that failed in this episode.
	attempt int
	// rateLimited counts the -328 replies in this episode.
	rateLimited int
	// shortSessions counts the consecutive sessions that ended within
	// shortSessionDuration of their login.
	shortSessions int
	// slow is set once the episode has left the episode cadence.
	slow bool
}

// begin marks the start of a login attempt at now.
func (s *recoverySchedule) begin(now time.Time) {
	if s.episodeAt.IsZero() {
		s.episodeAt = now
	}
}

// succeeded ends the episode after a successful login.
func (s *recoverySchedule) succeeded() {
	s.episodeAt = time.Time{}
	s.attempt = 0
	s.rateLimited = 0
	s.slow = false
}

// failed records a failed attempt at now and returns the delay before the
// next one, and whether this failure moved the episode to the slow tier.
func (s *recoverySchedule) failed(err error, now time.Time) (delay time.Duration, enteredSlow bool) {
	s.begin(now)
	s.attempt++
	wasSlow := s.slow
	var statusErr client.StatusError
	switch {
	case errors.As(err, &statusErr) && statusErr.Status == -328:
		s.rateLimited++
		if !s.slow && s.rateLimited <= len(rateLimitedRecoveryDelays) {
			delay = rateLimitedRecoveryDelays[s.rateLimited-1]
		} else {
			s.slow = true
			delay = slowRecoveryDelay
		}
	case !s.slow && now.Add(episodeRetryDelay).Before(s.episodeAt.Add(episodeWatchdog)):
		delay = episodeRetryDelay
	default:
		// The next attempt would run after the native watchdog.
		s.slow = true
		delay = slowRecoveryDelay
	}
	return delay, s.slow && !wasSlow
}

// sessionEnded starts a new episode after a session that lasted the given
// time. It returns the delay before the episode's first attempt, and whether
// this session moved reconnects to the slow tier.
func (s *recoverySchedule) sessionEnded(lasted time.Duration) (delay time.Duration, enteredSlow bool) {
	if lasted < shortSessionDuration {
		s.shortSessions++
	} else {
		s.shortSessions = 0
	}
	s.succeeded()
	if s.shortSessions > maxShortSessions {
		s.slow = true
		return slowRecoveryDelay, s.shortSessions == maxShortSessions+1
	}
	return firstRecoveryDelay, false
}
