package client

import (
	"context"
	"testing"
	"time"

	"github.com/frrad/mooo/internal/protocol/loco"
)

func TestParentEventsRechecksShutdownAdmission(t *testing.T) {
	for i := 0; i < 10000; i++ {
		raw := make(chan loco.Packet)
		close(raw)
		c := &Client{session: &session{pushes: raw}}
		start := make(chan struct{})
		eventsReturned := make(chan struct{})
		shutdownErr := make(chan error, 1)
		go func() { <-start; _, _ = c.Events(context.Background()); close(eventsReturned) }()
		go func() {
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			shutdownErr <- c.Shutdown(ctx)
		}()
		close(start)
		<-eventsReturned
		if err := <-shutdownErr; err != nil {
			t.Fatalf("iteration%d shutdown: %v", i, err)
		}
		c.mu.Lock()
		late := c.eventDone != nil
		c.mu.Unlock()
		if late {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = c.Shutdown(ctx)
			cancel()
			t.Fatalf("iteration%d decoder registered after successful shutdown cleared ownership", i)
		}
	}
}
