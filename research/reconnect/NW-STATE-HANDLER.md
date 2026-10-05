# LocoNWAgent network state handling

Status: reviewed static source chain, runtime unexecuted. Observation date:
2026-10-04. Client build: macOS KakaoTalk 26.8.0.

`LocoNWAgent` installs a weak-owner state callback while constructing an
`NWConnection`. The callback entry is `0x100d4a6b4`; it returns when the weak
owner cannot be loaded and otherwise forwards the state to
`0x100d464d4`. The setup path at `0x100d454d8` installs that callback, cancels
and releases the previous current connection when present, stores the
replacement, and starts it on its queue.

The dispatcher compares the actual `NWConnection.State` cases. `setup` and
`preparing` log only. `ready` logs TLS information and calls its ready
follow-up. `cancelled` conditionally cancels the stored receive work item,
constructs a `LocoAgent` error, and invokes `0x100d47a3c`, which dispatches a
main-queue block. The callback's unknown/default path logs only.

`failed` always invokes `handleConnectFailure(_:allowFallback:)` at
`0x100d45de8` with the observed allow-fallback argument set. The helper first
conditionally cancels the receive work item and, when a current connection is
present, clears its state handler, cancels it, and clears the stored reference.
It then enters the error path when allow-fallback is false, the immediate
failure predicate at `0x100d462f8` is true, the owner virtual fallback
predicate returns false, or either owner flag at the observed offsets is set.
That path converts the NW error, calls `setStatus:error:` on the owner,
enqueues the main-queue block through `0x100d47a3c`, then constructs the
`LocoAgent` error and calls `failPendingRequestsWithError:`. The order is
source-observed; these are separate status and pending-request effects. Otherwise it enters the V2SL fallback at
`0x100d47238`.

`waiting` does not unconditionally call the helper. After reading the current
NW path, it requires both owner flags to be clear, a nonnil path, and path
status `satisfied` or `requiresConnection`. It then evaluates the dispatcher error predicate (`0x100d460fc` or the
immediate-failure predicate at `0x100d462f8`); only that branch calls
`0x100d45de8`. The helper's owner fallback predicate is evaluated again
inside the helper. Other waiting callbacks log/return. These dispatcher
predicates and the helper's own guard are separate source stages.

The ready follow-up at `0x100d47508` logs TLS state, cancels and clears the
stored receive work item when present, sets the observed owner flag, and calls
`readHeader` after its guarded initial-send section. When the ready flag is enabled, it initializes a new V2SL crypto object and
calls `setV2slCrypto:` before checking the current connection. If a connection
exists, the crypto object (not the connection) supplies `handshakeData`; a nil
handshake becomes Optional.none, but the Network send still occurs with that
connection. Non-nil data is bridged as send content. The send uses
`contentProcessed`, default-message context, and `isComplete=true`. The
reviewed ready vector uses status value 3.
Its completion callback enters at `0x100d4998c` and forwards its captured
context to `0x100d47784`; this path does not perform a weak-owner load. It
filters POSIX error 0x59 before the remaining error/logging path. The exact
owner operation after that branch is an implementation boundary; no pending-map
lookup is visible in this callback.

The V2SL fallback at `0x100d47238` sets the fallback owner flag, attempts to
derive and validate an endpoint port, and calls the replacement setup path
when both are available. Missing or invalid endpoint data constructs an
NWError and routes through `0x100d47a3c`.

The main-queue thunk at `0x100d49960` captures the owner context and sends
`cancelPreviousPerformRequestsWithTarget:` through the NSObject receiver. This
is delayed-work cancellation for that owner, not a pending-request-map lookup.

The raw state path now proves an owner `setStatus:error:` call and a
`failPendingRequestsWithError:` fanout on the error path. It does not expose a
request-generation comparison or pending-map lookup, so correlation details
remain an explicit gap.

## Synthetic contract

`rc-q5-nw-state-handler.json` uses actual enum case names and input-derived
branches for owner lifetime, path presence/status, owner flags, immediate
failure, the helper fallback predicate, current/replacement connection
presence, dispatcher-error predicate, owner-fallback predicate, and
receive-work-item presence. It asserts ordered effects and keeps
setup replacement, waiting dispatch gating, failed-helper cleanup/decision, and
cancelled cleanup separate. Each case keeps pending-map correlation details as a gap; the observed
`setStatus:error:` and `failPendingRequestsWithError:` effects are asserted.
The fixture is static and synthetic; it does not activate a runtime transport
or claim server behavior.

## Provenance

- Private exact-address decompile:
  `nw-state-dispatch-20261004.txt` (private parity lab receipt), covering callback `0x100d4a6b4`, dispatcher `0x100d464d4`, setup `0x100d454d8`, helper `0x100d45de8`, TLS helper `0x100d46fd4`, error dispatch `0x100d47a3c`, and fallback `0x100d47238`.
- Private downstream decompile receipts `nw-state-downstream-20261004.txt` and
  `nw-state-downstream-blocks-20261004.txt`, covering ready follow-up
  `0x100d47508`, error dispatch block `0x100004760`, and completion callback `0x100d4998c` and handoff `0x100d47784`.
- Private arm64 disassembly of connection construction and callback installation around `0x100d454d8`.
