package client

import (
	"context"
	"time"
)

type admissionObserverKey struct{}

// WithAdmissionObserver returns a Context that reports, for each paced
// metadata command sent with it, how long the command waited for account
// admission before it went on the wire. A command that joins an identical
// in-flight request is not reported. The observer must not block.
func WithAdmissionObserver(ctx context.Context, observe func(command string, wait time.Duration)) context.Context {
	return context.WithValue(ctx, admissionObserverKey{}, observe)
}

func observeAdmission(ctx context.Context, command string, queued time.Time) {
	if observe, ok := ctx.Value(admissionObserverKey{}).(func(string, time.Duration)); ok && observe != nil {
		observe(command, time.Since(queued))
	}
}
