# PING lifecycle integration seam

Status: design-only contract for the approved RC-BIN-003 intent ordering,
2026-10-02. The source ledger is [`PROTOCOL.md`](PROTOCOL.md), with provenance
in [`EVIDENCE.md`](EVIDENCE.md). This document does not claim that the current
client starts a keep-alive timer or owns a PING supervisor.

## Boundary

The pure planner in `internal/protocol/sessionlogin/ping_intents.go` returns
ordered intents. A future session supervisor should own the target-specific
delayed invocation, cancellation handle, ordinary request dispatch, and
completion callback. The planner must remain independent of sockets, timers,
goroutines, and durable state.

The current `client.Session.Request` allocates a pending request, writes one
packet, waits for one result or context cancellation, and lets `readLoop`
remove and deliver the matching pending result. It has no delayed-PING owner or
callback-wrapper abstraction. Integrating the planner therefore requires an
explicit owner seam rather than silently inserting scheduling into the generic
request method.

## Proposed owner seam

The eventual owner can be tested against these operations without a real clock:

```text
QueueCancel(owner, selector)
QueueSchedule(owner, selector, generation)
DispatchOrdinaryRequest(request)
ForwardCompletion(response, error)
```

Only the `QueueCancel` and `QueueSchedule` operations are main-queue actions.
The ordinary request and completion forwarding operations execute inline in
the planner's returned order. A fake executor records queued actions and
exposes deterministic `RunNext` and `RunAll` operations. It stores a
generation/token on scheduled timer work; cancellation marks only matching
future timer work stale, and delivery checks that token immediately before
invoking the PING timer callback. This guard must not suppress an already
selected request-completion forwarding action or reorder scheduling relative
to that forwarding action.

Closing the session cancels future scheduled timer invocations, while the existing
disconnect fan-out resolves each in-flight request callback with one terminal
error. It must not strand those waiters or suppress a completion already in
progress. Timer callbacks and request callbacks are separate ownership
classes. These are testability decisions, not an observed timer
implementation.

The owner consumes planner results in order. It must preserve these approved
facts:

* accepted request entry queues cancellation before the ordinary request;
* early rejection queues cancellation and may forward its immediate opaque
  response/error when a callback exists, without completion-wrapper rearm;
* transport completion queues scheduling before forwarding either packet or
  error, with no nil/error predicate;
* a request with no completion queues cancellation, performs the ordinary
  request inline, then queues scheduling.

The fake executor tests both callback-present and callback-absent paths,
including nil response with nonnil error and nonnil response with nil error.
It also tests that a cancelled generation cannot fire after cancellation, that
close prevents a queued timer invocation, and that each callback is forwarded
at most once. These tests assert planned ordering and ownership only; they do
not assert elapsed time or network delivery.

## Admission and unresolved behavior

The initial condition that arms a keep-alive schedule, the interval clock, the
traffic-reset policy, and the complete timer start/stop lifecycle remain
unresolved in the public source record. They must be supplied by a separately
approved contract before the owner is connected to a real timer or session
constructor. The receive-header timeout path is also a separate owner and must
not be conflated with keep-alive scheduling.

The separate numeric connection-status callback has not been admitted to this
intent runner. Its identity comparison, latch initialization, status writes,
and callback gates require the final reviewed source ledger before a reducer or
integration code is added.

## Required implementation sequence

1. Keep the landed RC-BIN-003 source ledger and pure intent vectors executable.
2. Add a fake queue executor and owner-level conformance tests for the order,
   generation cancellation, close handling, and callback-once rules above.
3. Review and approve the initial-arm and clock contracts separately.
4. Only then connect the owner to a real session supervisor; do not add timer
   behavior to `Session.Request` or `readLoop` as an implicit side effect.
