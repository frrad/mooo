# Receive-header timeout contract

Status: reviewed static contract, 2026-10-02. This specification covers the
receive-header delayed selector and its failure handoff. It deliberately keeps
socket read timeouts, OS queue execution timing, and pending-map mutation
outside the observed contract.

The timeout helper receives a timeout value, an owner, an enable flag, and a
signed request tag. It first reads the owner's `receiveHeaderTimeout` value and
requires both a positive timeout and a nonnegative signed 64-bit tag. A failed
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
socket delegate receives `(agent, socket, error)`, sets the agent status to
`0` with that error, cancels prior delayed-selector work for the agent target,
and enumerates the agent's pending-completion map. Each enumerated completion
is called with `(nil, NSError(domain="LocoAgent", code=-1,
userInfo=nil))`. The reviewed handler does not explicitly clear the pending
map; whether `setStatus:error:` mutates or replaces it is unresolved. The
handler's POSIX-domain/code 60 or 32 logging branches are separate from the
fanout and do not alter this contract. No socket-identity guard was observed
in the reviewed handler.

The packet producer arms the receive-header timeout only on its status-3
send path after deriving the packet tag and sending the packet. If the owner
status is not 3, a supplied completion receives the producer's error and the
header timeout is not armed. A separate completion-side helper disarms only
when the pending packet found by unsigned packet ID has a matching unique ID;
it then derives the request tag and disables that exact timeout. No generic
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
