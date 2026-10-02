# Reconnect keep-alive and receive-timeout boundary

Status: static handoff with explicit gaps, 2026-10-02. Evidence: RC-BIN-001.

The official carriage has a manager-owned periodic keep-alive. A timer callback
checks whether the elapsed time since the last ping exceeds the configured
carriage ping interval. When it does, the manager constructs a zero-field
`PING` request and sends it through the ordinary carriage request path. The
request completion is forwarded through the normal response wrapper; a
successful reply does not expose any additional application state. The
completion cancels previously scheduled keep-alive invocations, avoiding
duplicate scheduled pings.

The timer is separate from packet receive processing. The traced coordinator timer compares elapsed time using Foundation's
`timeIntervalSinceReferenceDate` wall/reference-date clock. This does not establish
monotonic behavior, so wall-clock jumps remain an implementation concern. The
coordinator assigns a fallback ping interval of 180 seconds: any positive signed
32-bit configuration value is retained, while zero or negative values become 180.
Timer creation/start gate, timer stop gate, and whether other traffic updates the
last-ping timestamp were not reachable through direct call edges in the analyzed
build. They remain gaps rather than defaults. A separate
Swift helper in the binary also builds an HTTP `/ping` request; it is excluded from
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

Observed: zero-field PING construction, ordinary carriage dispatch, completion
wrapper (nil response calls the supplied completion with nil; non-nil response is
wrapped before that call), delayed receive-header timeout gated by positive timeout
and request tag, timeout-to-disconnect, tag-driven header/body read callbacks,
configuration values at the traced initializer, and reference-date elapsed-time comparison in the
coordinator timer.

Implementation decisions: represent PING scheduling with an injected clock and
make wall-clock behavior explicit until a stronger source is recovered;
fail pending requests once on timeout-disconnect; keep keep-alive and receive
timeout ownership in the session supervisor rather than the wire parser.

Untraced: timer lifecycle gates, traffic reset policy, exact timeout error value,
whether idle sessions arm the receive timeout, and whether configuration callers
override the recovered defaults.
