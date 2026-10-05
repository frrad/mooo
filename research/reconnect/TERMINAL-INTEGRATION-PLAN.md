# Terminal recovery integration plan

Status: production consumer audit, 2026-10-04. No terminal reset or route
mutation is implemented by this plan.

## Findings

The typed event decoder already emits `events.ChangeServer` and
`events.Kickout`. The pure `sessionlogin.ReduceRecovery` reducer accepts both
events, checks the supplied generation, rejects terminal duplicates, and emits
transport-neutral effects. The reducer's CHANGESVR order is route clear then
change-server logout. Its KICKOUT guard is authenticated-session only, and
reasons 1 and 10 append a reset effect after logout.

The production bridge does not consume those effects. `KakaoClient.run` marks a
KICKOUT as a bad-credentials terminal state, then passes both terminal events
to `handleEvent`; `remoteEventFor` has no terminal mapping, so neither event is
committed or routed to recovery. A CHANGESVR therefore remains an ignored event
and a later stream close is reported as an ordinary transient disconnect. The
bridge's `kakaoClient` interface also exposes no route-clear, logout, reset, or
recovery-generation operation.

The Session event stream does not close a terminal stream from the carriage by
itself: the reader forwards the typed notice to `Client.Events`, and only a
transport close closes the raw push channel. Without an owner action, the
profile session remains usable for `SendText` and the profile lease remains
held. A synthetic `net.Pipe` carriage reproduced this path. The client now
marks itself closed and interrupts its owned Session when the typed decoder
recognizes CHANGESVR or KICKOUT. Admission closes before the terminal event is
published, so `SendText` and `CommitEvent` fail immediately. The checkpoint and
profile lease remain retained until the caller invokes `Shutdown`, which joins
any session worker before releasing ownership. This is bounded
transport/session shutdown only; it does not clear routes, reset storage, or
reconnect. Raw `Pushes` remains caller-owned and does not apply this
typed-decoder shutdown policy.

This leaves two integration gaps:

1. CHANGESVR needs an injected manager/recovery effect boundary that can clear
   the cached route and perform the ordered logout before any fresh booking is
   considered. The source contract does not authorize an automatic retry.
2. KICKOUT needs one guarded terminal operation that always owns the logged-in
   and logging-out checks and logout. Its reset argument is true only for
   reasons 1 and 10. Splitting `EffectLogout` and `EffectResetDatabase` across
   unrelated bridge calls could violate the traced
   `logoutWithResetDatabase:` ordering and storage guards.

The current generation checks prevent reducer callbacks from changing a
terminal state, but terminal effects still carry the pre-terminal generation.
The eventual executor must reject queued effects after terminal shutdown and
must invalidate pending recovery work before route or profile mutation. No
generation increment is added here because the correct ownership boundary for
that invalidation is not yet present in `client.Client`.

## Reviewable next slice

Add an injected terminal consumer to the client/bridge seam with two explicit
operations: `ChangeServer(ctx)` and `Kickout(ctx, reason)`. The consumer should
receive typed events from the single event-loop goroutine and return before the
terminal state is published. It must preserve ordinary push ordering and must
not map terminal notices to bridge remote events.

The first implementation should be effect-only and test-backed. The bridge now
reports an admitted CHANGESVR as a distinct transient-disconnect error after
the stream ends, preserving the no-auto-reconnect policy; this is a reporting
boundary, not route mutation.

Once either terminal notice is observed, later stream events are discarded
until the stream closes. Repeated terminal notices are therefore idempotent,
and a normal message cannot be committed after terminal ownership has begun.
An explicit `Disconnect` still wins over terminal reporting through the
existing stopping guard, so shutdown does not emit a second state.

* CHANGESVR records clear-route, ticket-cursor selection, logout, and carriage
  disconnect in source order; no automatic reconnect is inferred.
* KICKOUT rejects unauthenticated or already-logging-out input, records the
  reason, and invokes one combined logout/reset operation with reset true only
  for reasons 1 and 10. Optional notification projection remains a separate
  callback.
* A stale generation and a terminal duplicate produce no external operation.
* A stream ending after CHANGESVR reports the terminal result rather than the
  generic transient-disconnect error; a KICKOUT reports bad credentials only
  after its terminal operation accepts the event.

The callback-return-before-state-publication ordering is a proposed integration
policy until failure and acceptance behavior are traced. Required storage and route contracts must be approved before wiring database
deletion, observer/UI notifications, or automatic booking. Until then, the
bridge should continue its current safe behavior for those unresolved effects
and expose the gap through tests rather than guessing side effects.
