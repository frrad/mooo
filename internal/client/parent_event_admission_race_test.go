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
		returned := make(chan error, 2)
		go func() { <-start; _, e := c.Events(context.Background()); returned <- e }()
		go func() {
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			returned <- c.Shutdown(ctx)
		}()
		close(start)
		<-returned //nolint:errcheck // waits for completion; the result is not under test
		<-returned //nolint:errcheck // waits for completion; the result is not under test
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
