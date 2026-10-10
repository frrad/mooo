package client

import (
	"context"
	"time"

	"github.com/frrad/mooo/internal/authstate"
	"github.com/frrad/mooo/internal/continuity"
	"github.com/frrad/mooo/internal/protocol/events"
	"github.com/frrad/mooo/internal/protocol/loco"
)

// Test-only conveniences over production entry points. They pass fixed
// defaults to the production functions they wrap.

func decodeEventStreamWithContinuity(raw <-chan loco.Packet, output chan<- events.Result, checkpoint *continuity.Store, delivered func(int64, int64)) {
	decodeEventStreamWithTerminal(raw, output, checkpoint, delivered, nil)
}

func decodeEventStreamWithTerminal(raw <-chan loco.Packet, output chan<- events.Result, checkpoint *continuity.Store, delivered func(int64, int64), terminal func()) {
	decodeEventStreamWithTerminalStop(raw, output, checkpoint, delivered, terminal, nil)
}

func connectSessionWithDialers(ctx context.Context, state authstate.State, dialers sessionDialers) (*Session, error) {
	return connectSessionWithResume(ctx, state, continuity.Checkpoint{Version: continuity.Version}, dialers)
}

func (s *Session) readLoop() {
	s.mu.Lock()
	if s.readLoopStarted {
		s.mu.Unlock()
		return
	}
	s.readLoopStarted = true
	s.mu.Unlock()
	s.readLoopBody()
}

// newRealtimePingTimerOwner supplies the Go relative-clock adapter to callers
// that have already selected an interval. It does not choose bootstrap or
// configuration policy.
func newRealtimePingTimerOwner(interval time.Duration, callback func()) *pingTimerOwner {
	return newPingTimerOwner(realtimeTimer{}, interval, callback)
}
