# Send-order implementation boundary

The pure `sessionlogin.PlanSendOrder` planner implements the reviewed RC-BIN-024
branch ordering from `rc-q5-send-order.json`. It returns typed effects carrying
the packet tag, packet unique ID, and only the proven nil-packet/error-presence
tuple. It performs no socket, encryption, timeout, or completion work.

The eventual consumer belongs at the authenticated carriage request owner. It
must register the completion and packet-ID association, including the same
packet tag, before the send path; execute packet serialization/encryption and
socket submission with timeout `-1`; enable the out-segment timeout; then hand
the packet tag to the receive-header timeout owner after `sendPacket` returns.
This ordering is around submission returning, not the later asynchronous write
completion. That owner is the separate injected seam from the receive-header
timeout work; this planner does not call or replace it.

The non-status-3 completion effect is one callback tuple carrying a nil packet
and an opaque producer error; it is not two callbacks. The remaining
integration inputs are explicit: the current request owner must supply the
producer status, completion identity, crypto presence, and packet tag; the
socket writer must report its completion without reordering the planned
submission effects; and the response consumer must preserve the existing
method/ID correlation. Write completion and partial-write failure behavior,
packet serialization fields, encryption-prefix endianness/failure, and
producer error identity remain untraced and are intentionally outside this
planner.
