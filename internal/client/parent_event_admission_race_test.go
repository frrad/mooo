package client

import (
	"context"
	"github.com/frrad/mooo/internal/protocol/loco"
	"testing"
	"time"
)

func TestParentEventsRechecksShutdownAdmission(t *testing.T) {
	for i := 0; i < 10000; i++ {
		raw := make(chan loco.Packet)
		close(raw)
		c := &Client{session: &Session{pushes: raw}}
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
		<-returned
		<-returned
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
