# LocoNWAgent Network state handling

Status: reviewed static source chain, runtime unexecuted. Observation date:
2026-10-04. Client build: macOS KakaoTalk 26.8.0.

`LocoNWAgent` installs a weak-owner state callback while constructing an
`NWConnection`. The callback entry is `0x100d4a6b4`; it weak-loads the owner
and returns without dispatch when the owner is gone. The callback forwards the
`NWConnection.State` value to `handleStateUpdate(_:)` at `0x100d464d4`.

The state dispatcher compares the actual Network enum cases. `waiting` and
`failed` extract their associated `NWError` and call the connection-failure
helper `0x100d45de8`; `setup` and `preparing` only log. `ready` calls the TLS
logging helper `0x100d46fd4` and then its ready follow-up. `cancelled` logs,
cancels the stored receive work item, creates a `LocoAgent` error, and invokes
`0x100d47a3c`, which dispatches a main-queue block. An unrecognized enum path
only logs. The helper's fallback decision accepts the captured allow-fallback
flag and calls `0x100d47238` when its own predicates pass.

The connection replacement path at `0x100d454d8` creates the weak-owner
callback, installs it, cancels/releases the previously stored current
connection, stores the replacement, and starts it on a queue. The failure
helper `0x100d45de8` cancels the stored receive work item, clears the current
connection's state handler, cancels/releases that current connection, and
then either constructs an error and dispatches `0x100d47a3c` or enters the
fallback path. These are distinct from the callback dispatcher itself.

The raw state path exposes no pending-request map lookup, request-generation
comparison, or direct status publication. Base-carriage pending fanout and
status effects remain a separate source boundary; the synthetic contract
keeps that downstream chain explicitly unresolved.

## Synthetic contract

`rc-q5-nw-state-handler.json` uses actual enum case names, owner lifetime,
current/replacement connection identities, and allow-fallback input. It
asserts the state-specific ordered effects and exact current-connection
identity used by setup replacement. Every case marks pending-map/status fanout
as a gap. The fixture is static and synthetic; it does not activate a runtime
transport or claim server behavior.

## Provenance

- Private exact-address decompile:
  `nw-state-handler-20261004.txt`, covering `0x100d4a6b4`, `0x100d464d4`,
  `0x100d454d8`, `0x100d45de8`, `0x100d46fd4`, `0x100d47a3c`, and
  `0x100d47238`.
- Private arm64 disassembly of the connection construction and callback
  installation around `0x100d454d8`.
