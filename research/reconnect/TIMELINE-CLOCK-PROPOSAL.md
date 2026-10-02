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
request IDs; those calls are transport concerns and are not a reconnect clock.
Bridge connector tests currently use real deadlines for lifecycle assertions.

## Bounded interface proposal

The supervisor should receive a timeline dependency at construction rather than
calling `time.Now`, `time.After`, or `time.Sleep` directly. The smallest useful
boundary is:

```go
type Timeline interface {
    Now() time.Duration
    Schedule(delay time.Duration, generation uint64) TimerHandle
}
type TimerHandle interface {
    Cancel()
}
```

`Now` is an elapsed timeline value supplied by the owner. `Schedule` returns a
handle that can be cancelled on success, terminal action, shutdown, or a newer
generation. The timer callback must inject a typed `RecoveryTimerFired{Generation}`
event into the reducer; it must not call login or mutate state itself. The
reducer remains usable without a timeline by continuing to emit a schedule
intent as an effect. This proposal intentionally leaves the timeline's source
(monotonic uptime, wall clock, or process uptime) unresolved because the public
questions do not establish it.

The eventual effect should carry an explicit delay and generation, for example
`ScheduleRecovery{Generation, Delay}`. Applying that effect is the only place
that may call `Timeline.Schedule`. A timer event with an old generation is
rejected as stale, and cancellation is idempotent. No timer is owned by the
carriage layer.

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
growth/reset rule, maximum attempts, timer clock, sleep/wake behavior, or
reachability interaction. They also do not establish whether token renewal and
`-328` retry share a budget. Until those values are transferred as a reviewed
contract, implementing a concrete timer or delay would be speculative.
