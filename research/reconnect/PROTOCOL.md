# Reconnect keep-alive and receive-timeout boundary

Status: static lifecycle supplement with explicit gaps, 2026-10-02. Evidence:
RC-BIN-001 through RC-BIN-003.

The manager exposes a configurable carriage ping interval and a method that
constructs a zero-field `PING` request through the ordinary carriage request
path. Three recovered manager callback paths read that interval and schedule a
delayed invocation of the carriage ping method on the carriage target. A
paired callback cancels prior delayed invocations for that same target and
selector. The entry/exit gates for those callbacks and any traffic-based reset
remain untraced. The request completion is forwarded through the normal
response wrapper; a successful reply does not expose any additional
application state. The traced completion itself does not cancel a keep-alive
timer.

The concrete PING method allocates an empty carriage packet and submits it
through the manager's ordinary carriage request path. That ordinary path
queues a main-queue cancellation of any delayed PING invocation for the same
request owner before continuing with its normal request/error handling. The
cancellation is queued rather than executed inline, so ordering against a
simultaneously eligible delayed invocation is not proven. The cancellation is
queued even when the request-owner status check takes the early failure path.
The source chain does not prove that this path immediately re-arms the
interval.

The traced request-completion callback posts interval-based PING scheduling to
the main queue before forwarding either packet or error arguments to the
supplied completion. The callback has no nil/error predicate in this wrapper,
so both completion forms follow the same ordering. A request-owner status
rejection queues cancellation but does not enter this completion wrapper and
therefore does not queue its re-arm. The no-completion request path queues
cancellation, invokes the ordinary request method, and then queues scheduling.

A separate status callback is guarded by request-owner identity. Its observed
numeric branches are status `0` (disconnect bookkeeping followed by queued
PING cancellation) and status `3` (active-flag update); their external labels
are not established by this trace.

The coordinator assigns a fallback ping interval of 180 seconds: any positive
signed 32-bit configuration value is retained, while zero or negative values
become 180. The interval setter, storage field, and delayed scheduling
primitive are proven, but the caller gates that start or stop those callbacks
were not recovered. Timer creation/start gate, timer stop gate, elapsed-clock
source, and whether other traffic updates the last-ping timestamp remain gaps.
A separate Swift helper in the binary has a
Foundation-clock timer that builds an HTTP `/ping` request; it is excluded from
this LOCO carriage contract.

The carriage read loop is tag-driven. A header read completion enters the
header parser; a body completion enters the body parser; a V2 secure-layer
completion enters the secure-layer path. The receive-header timeout helper is
armed only when the configured timeout is positive and the supplied signed tag is
non-negative. It schedules a delayed timeout callback on the main
queue. The callback invokes carriage disconnect. The helper also supports
cancelling a prior delayed timeout for the same target/selector. A timeout is
therefore a carriage disconnect and consequently fails all pending callbacks
through the already established disconnect fan-out.

The manager's booking/configuration initializer passes connect=15,
receive-header=20, in-segment=10, and out-segment=10 seconds to its downstream
configuration object. This proves the values at that call site, not that every
socket construction uses them; later override or alternate-constructor behavior
was not recovered. The complete
arm/disarm call sites in the socket read loop and whether the timeout remains armed
with no pending request were not proven. Per-request timeout semantics and
body-read timeout behavior are likewise open.

## Transfer boundaries

Observed: zero-field PING construction through the ordinary carriage request
path, queued cancellation of a delayed PING for the same request owner when
that path is entered (including its early failure path), request completion
that queues interval-based scheduling before forwarding packet/error arguments,
no-completion request ordering of cancel -> request -> schedule, the separate
numeric status `0` and `3` branches, interval read plus delayed PING scheduling,
cancellation of a prior delayed PING for the same target and selector,
completion wrapper (nil response calls the supplied
completion with nil; non-nil response is wrapped before that call), delayed
receive-header timeout gated by positive timeout and request tag,
timeout-to-disconnect, tag-driven header/body read callbacks, configuration
values at the traced initializer, and the separate HTTP helper's
Foundation-clock `/ping` chain as an excluded path.

Implementation decisions: represent PING scheduling with an injected clock only
after the LOCO clock source is proven; keep the LOCO clock source explicitly
unknown while it remains untraced;
fail pending requests once on timeout-disconnect; keep keep-alive and receive
timeout ownership in the session supervisor rather than the wire parser.

Untraced: timer lifecycle gates around the recovered scheduling callbacks,
traffic reset policy, exact timeout error value, whether idle sessions arm the
receive timeout, and whether configuration callers override the recovered
defaults.

## Synthetic vector status

The executable pure-policy vectors are in
`internal/protocol/sessionlogin/testdata/reconnect/rc-q4-q5.json`; the loader
rejects unknown fields and fails on unsupported pure-policy kinds. Wire-shape,
completion-forwarding, and disconnect/failure-order vectors remain separate and
explicitly unexecuted in
`internal/protocol/sessionlogin/testdata/reconnect/rc-q4-q5-unresolved.json`.
The vector evidence also identifies RC-BIN-002 for the recovered scheduling
primitive without treating its lifecycle or clock as implemented.
