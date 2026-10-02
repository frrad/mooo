# Receive-header timeout contract

Status: reviewed static contract, 2026-10-02. This specification covers the
receive-header delayed selector and its failure handoff. It deliberately keeps
socket read timeouts, OS queue execution timing, and pending-map mutation
outside the observed contract.

The timeout helper is an owner method receiving an enable flag and a signed
request tag. It first reads the owner's `receiveHeaderTimeout` value and
requires both a positive timeout and a nonnegative signed 64-bit tag. The producer's request-tag helper is an identity return of the packet identifier: its unsigned 32-bit input is zero-extended into the signed tag domain, so packet IDs remain nonnegative. A failed
gate performs no main-queue enqueue. A passing gate enqueues work on the main
queue with a weak owner. The queued work compares the enable byte to exactly
`true` (`1`). On enable it rereads `receiveHeaderTimeout` from the captured
owner, wraps the signed tag as an NSNumber, and invokes the delayed-selector
API on that owner using `fireReceiveHeaderTimeout:` and the wrapped tag. The
second getter read is intentional: a configuration change between admission
and queue execution can change the delay. On disable it cancels the matching
owner/selector/object tuple using the same owner, `fireReceiveHeaderTimeout:`
selector, and tag object. Main-queue execution and cancellation races remain
OS behavior gaps.

When the delayed selector fires, the owner sends itself `disconnect`. The
owner's disconnect method queues a socket disconnect on its object queue. The
socket delegate receives `(agent, socket, error)`. It writes status byte `0`
with that error through `setStatus:error:`; when a status handler is installed,
the setter synchronously invokes it with `(agent, oldStatus, newStatus, error)`
before the disconnect path cancels prior delayed-selector work for the agent
target and enumerates the pending-completion map. The handler may mutate state,
including pending-map state, before cancellation/fanout; that indirect effect
is unresolved. With no handler, the setter has no callback effect. Each
enumerated completion is called with `(nil, NSError(domain="LocoAgent", code=-1,
userInfo=nil))`. The reviewed handler does not explicitly clear the pending map.
Its POSIX-domain/code 60 or 32 logging branches are separate from fanout. No
socket-identity guard was observed in the reviewed handler.

The packet producer arms the receive-header timeout only on its status-3
send path. Its block carries a weak agent reference and a separate strong
scheduling-owner capture. The creator initializes both from the same request
owner; block copying retains the request and scheduling-owner fields and copies
the weak field, while disposal releases/destroys those fields. The invoke body
performs no identity comparison, so preserve the distinct capture lifetimes
while retaining the same-owner creation fact. When a completion exists, it first registers that completion in the
agent's unique-id map and stores the unique-id string in the packet-id map; it
then sends the packet and arms the timeout. A successful status-3 path does not
invoke the supplied completion immediately. If the owner status is not 3, a
supplied completion receives the producer's error and the header timeout is not
armed; with no completion there is no callback effect. A separate completion-side helper looks up the unsigned packet ID in the
agent's packet map. That map stores the request unique-id string as its value.
Both this helper and the ordinary response consumer obtain the comparison value
from the packet's `header` object's `uniqueId` accessor. The accessor constructs
its value as the packet method and unsigned packet ID formatted with the exact
`"%@.%@"` template. Synthetic vectors use ordinary method strings; the valid
method domain and nil-method behavior are untraced and must remain explicit
inputs in an independent implementation. The reviewed layer does not establish
how that header object is decoded from wire bytes, nor whether a secure-prefix
read precedes this accessor; those are separate integration gaps.
The helper disarms only when that stored string equals the incoming packet
unique-id; missing or nonmatching values produce no timeout action. Equality
then derives the request tag by that packet-ID identity mapping and disables that exact timeout. No generic
read-loop or idle transition was found to arm or disarm this helper. Header
timeout behavior is therefore distinct from socket read/in-segment timeout
handling.

The manager PING request path is a separate caller chain. Ordinary request
entry queues cancellation of the delayed PING for the same captured request
owner before admission/transport handling; request completion queues a new
relative PING schedule before forwarding packet or error arguments. A status-0
callback queues cancellation. This document does not assert that timeout
failure itself rearms PING or that the pending map is cleared.

Implementation decisions for an independent client are to model the reviewed
ordered effects with an injected relative scheduler, keep owner/selector/tag
identity explicit, and choose a terminal pending-request failure policy. Those
choices do not describe unobserved Foundation queue timing or durable manager
state.
