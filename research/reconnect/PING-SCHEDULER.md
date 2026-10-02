# LOCO delayed PING scheduler boundary

Status: static source supplement, 2026-10-02. This records the observed
relative scheduler API and caller ordering; it does not claim to reproduce the
OS queue's execution clock.

The traced configuration callback reads `fgPingItv` from the object returned by
`getConfWifi`, whose static accessor is a signed 32-bit field, converts it to a
signed integer, substitutes 180 seconds when
the value is below 1, and passes the result to the manager interval setter.
The manager setter stores the supplied interval directly; the below-one
fallback belongs to this caller. This source mapping is specific to the traced
callback. The model is archived in the shared defaults store under a key formed
from `GETCONFWIFI:%@` and the profile hash input; a missing object returns no
model. The archived field is signed 32-bit. Other booking paths are not assumed
to consume the same configuration field. The scheduler
reads the stored interval and invokes the captured request-owner target with
`performSelector:withObject:afterDelay:`. The selector is the manager's PING
method and the object argument is nil.

Cancellation uses `cancelPreviousPerformRequestsWithTarget:selector:object:`
with the same request-owner target, the same PING selector, and nil object.
This target/selector/object tuple is the cancellation identity. Cancellation is
queued on the main queue by request, push-receipt, and status-zero
callers; queued cancellation is not synchronous with the caller.

The PING method allocates an empty packet and enters the carriage request path.
For an ordinary request, the path queues cancellation before its carriage-agent
admission and transport dispatch. A normal transport completion queues a new
relative PING invocation before forwarding packet/error arguments. A
no-completion request queues cancellation, invokes ordinary transport inline,
and queues scheduling. A push-receipt path has the same cancel → send → schedule
order. The status-zero branch queues cancellation and clears the status handler;
an early request rejection queues cancellation but does not enter the ordinary
completion wrapper.

Observed: `getConfWifi` → `fgPingItv` configuration mapping and fallback at the
traced callback, relative delay API, interval getter, target/selector/object identity,
cancellation call, main-queue enqueue boundaries, PING empty-packet construction,
and caller-specific cancel/schedule ordering.

Implementation decision: expose a relative scheduler interface and inject a
monotonic clock in the Go client. That clock is a clean-room runtime design,
not a claim about the official client's internal clock. Preserve queued
cancellation and scheduling as ordered events; allow an explicit scheduler
implementation to define execution races.

Untraced: exact OS queue execution timing, whether an already-eligible delayed
invocation can run before a queued cancellation, global first-admission ordering
among concurrent traffic, and any alternate scheduler caller outside the
traced paths.
