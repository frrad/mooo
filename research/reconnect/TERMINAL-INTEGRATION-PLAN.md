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

This leaves two integration gaps:

1. CHANGESVR needs an injected manager/recovery effect boundary that can clear
   the cached route and perform the ordered logout before any fresh booking is
   considered. The source contract does not authorize an automatic retry.
2. KICKOUT needs one guarded terminal operation that owns the logged-in and
   logging-out checks, logout, and the reason-1/10 reset decision. Splitting
   `EffectLogout` and `EffectResetDatabase` across unrelated bridge calls could
   violate the traced `logoutWithResetDatabase:` ordering and storage guards.

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

The first implementation should be effect-only and test-backed:

* CHANGESVR records clear-route, ticket-cursor selection, logout, and carriage
  disconnect in source order; no automatic reconnect is inferred.
* KICKOUT rejects unauthenticated or already-logging-out input, records the
  reason, and invokes one combined logout/reset operation only for reasons 1 and
  10. Optional notification projection remains a separate callback.
* A stale generation and a terminal duplicate produce no external operation.
* A stream ending after CHANGESVR reports the terminal result rather than the
  generic transient-disconnect state; a KICKOUT reports bad credentials only
  after its terminal operation accepts the event.

Required storage and route contracts must be approved before wiring database
deletion, observer/UI notifications, or automatic booking. Until then, the
bridge should continue its current safe behavior for those unresolved effects
and expose the gap through tests rather than guessing side effects.

