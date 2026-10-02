# Reconnect timeline and clock proposal

Status: design audit, 2026-10-02. This document proposes test seams only; it does
not select an observed delay, clock basis, or production timer implementation.

## Existing seams

`internal/protocol/sessionlogin.ReduceRecovery` is already a pure reducer. It
admits a generation, returns `EffectScheduleRecovery` after a failed attempt,
and rejects stale completions. The effect is data only; no timer or goroutine is
started there. `internal/client.Client` explicitly does not reconnect after a
session disconnect, so a future supervisor must own retry scheduling. The
carriage/session code uses wall-clock `time.Now()` only for socket deadlines and
request IDs; those calls are transport concerns and are not a reconnect clock. Foundation timer tracing reaches an HTTP `/ping` helper outside the LOCO carriage contract; LOCO keep-alive timer ownership and clock remain untraced.
Bridge connector tests currently use real deadlines for lifecycle assertions.

## Bounded interface proposal

The supervisor should receive a retry scheduler dependency at construction rather
than calling `time.After` or `time.Sleep` directly. Keep this interface separate
from the other time domains:

```go
type RetryScheduler interface {
    ScheduleRetry(delay time.Duration, generation uint64) RetryTimer
}
type RetryTimer interface {
    Events() <-chan RetryTimerEvent
    Cancel()
}
type RetryTimerEvent struct { Generation uint64 }
```

`Events` is the observable delivery path: the owner reads one event and would
need a future reducer event (for example `RecoveryTimerFired{Generation}`) because
no such event is currently accepted by `ReduceRecovery`. The timer callback never
calls login or mutates reducer state. `Cancel` is idempotent and closes or drains
the event stream according to the scheduler contract, so a cancelled timer
cannot admit a retry. A deterministic fake scheduler can retain due times,
advance explicitly, deliver exactly one event at due time, and verify cancelled
or stale generations without sleeping.

This is deliberately retry-only. `EndpointCache` keeps its existing elapsed
`time.Duration` uptime input. Socket deadlines continue to use absolute
`time.Time` at the transport boundary. Foundation HTTP `/ping` timing is outside the LOCO contract. LOCO keep-alive timing
remains untraced, and none of these domains should be hidden behind a single `Now`
method or converted into retry timestamps.

The current reducer remains usable without a scheduler by emitting a schedule
intent as data. Adding a timer-fired event and a delay field is a future API
change, not an assumption about the current `RecoveryEvent` interface. The eventual effect should carry an explicit retry delay and generation,
for example `ScheduleRecovery{Generation, Delay}`. Applying that effect is the
only place allowed to call `RetryScheduler.ScheduleRetry`. The constructor's
scheduler/default delay remains an integration decision pending public source
vectors.

## Synthetic conformance cases

Once the source answers the delay and clock questions, a fake timeline can
advance deterministically and assert:

1. a failed admitted attempt emits exactly one schedule intent with its current
generation and reviewed delay;
2. advancing before the due point emits no timer event;
3. advancing to due injects one timer event and admits at most one retry;
4. success or terminal `CHANGESVR`/`KICKOUT` cancels the pending handle;
5. a stale timer event produces no login attempt and no new schedule;
6. shutdown and duplicate cancellation do not panic or reschedule; and
7. a network-restored event either shortens or preserves the pending delay only
   after that behavior is established by the source contract.

These cases require no sockets, credentials, or live account. They should be
added under `internal/protocol/sessionlogin` as policy vectors first, then to a
supervisor integration harness only after the full reconnect chain is reviewed.

## Open evidence gaps

The public reconnect questions still do not establish the first retry delay,
growth/reset rule, maximum attempts, retry timer constructor/defaults, sleep/wake
behavior, or reachability interaction. The retry clock is therefore unresolved
separately from the excluded Foundation HTTP `/ping` timer. They also do not establish whether token renewal and
`-328` retry share a budget. Until those values are transferred as a reviewed
contract, implementing a concrete timer or delay would be speculative.
