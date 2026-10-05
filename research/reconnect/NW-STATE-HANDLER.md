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
That path converts the NW error, constructs the LocoAgent error, and dispatches
`0x100d47a3c`. Otherwise it enters the V2SL fallback at `0x100d47238`.

`waiting` does not unconditionally call the helper. After reading the current
NW path, it requires both owner flags to be clear, a nonnil path, and path
status `satisfied` or `requiresConnection`. It then evaluates the observed
fallback predicate and immediate-failure predicate; only the passing branch
calls `0x100d45de8`. Other waiting callbacks log/return. These dispatcher
predicates and the helper's own guard are separate source stages.

The raw state path exposes no pending-request map lookup, request-generation
comparison, or direct status publication. Pending fanout and status delivery
remain an explicit downstream source gap; this contract does not infer them
from the main-queue error dispatch.

## Synthetic contract

`rc-q5-nw-state-handler.json` uses actual enum case names and input-derived
branches for owner lifetime, path presence/status, owner flags, immediate
failure, the helper fallback predicate, current/replacement connection
presence, and receive-work-item presence. It asserts ordered effects and keeps
setup replacement, waiting dispatch gating, failed-helper cleanup/decision, and
cancelled cleanup separate. Each case marks pending-map/status fanout as a gap.
The fixture is static and synthetic; it does not activate a runtime transport
or claim server behavior.

## Provenance

- Private exact-address decompile:
  `/Users/frederick/Library/Application Support/mooo-lab/ghidra/parity/nw-state-dispatch-20261004.txt`, covering callback `0x100d4a6b4`, dispatcher `0x100d464d4`, setup `0x100d454d8`, helper `0x100d45de8`, TLS helper `0x100d46fd4`, error dispatch `0x100d47a3c`, and fallback `0x100d47238`.
- Private arm64 disassembly of connection construction and callback installation around `0x100d454d8`.
