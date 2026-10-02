# Reconnect keep-alive and receive-timeout boundary

Status: static handoff with explicit gaps, 2026-10-02. Evidence: RC-BIN-001.

The manager exposes a configurable carriage ping interval and a method that
constructs a zero-field `PING` request through the ordinary carriage request
path. The recovered interval setter and request method do not by themselves
prove the timer consumer or lifecycle. The request completion is forwarded
through the normal response wrapper; a successful reply does not expose any
additional application state. The traced completion itself does not cancel a
keep-alive timer.

The coordinator assigns a fallback ping interval of 180 seconds: any positive
signed 32-bit configuration value is retained, while zero or negative values
become 180. The interval setter and storage field are proven, but the consumer
that schedules LOCO keep-alive work was not recovered. Timer creation/start gate,
timer stop gate, elapsed-clock source, and whether other traffic updates the
last-ping timestamp remain gaps. A separate Swift helper in the binary has a
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

Observed: zero-field PING construction, ordinary carriage dispatch, completion
wrapper (nil response calls the supplied completion with nil; non-nil response is
wrapped before that call), delayed receive-header timeout gated by positive timeout
and request tag, timeout-to-disconnect, tag-driven header/body read callbacks,
configuration values at the traced initializer, and the separate HTTP helper's
Foundation-clock `/ping` chain as an excluded path.

Implementation decisions: represent PING scheduling with an injected clock and
make wall-clock behavior explicit until a stronger source is recovered;
fail pending requests once on timeout-disconnect; keep keep-alive and receive
timeout ownership in the session supervisor rather than the wire parser.

Untraced: timer lifecycle gates, traffic reset policy, exact timeout error value,
whether idle sessions arm the receive timeout, and whether configuration callers
override the recovered defaults.
