# Official-client protocol parity

Status: active Ghidra-first parity program, 2026-09-29.

This program replaces request-constructor-only reversing with a repeatable review
of each feature's complete official-client behavior. The current macOS 26.8.0
binary is the primary first-party source. Raw decompiler output, addresses, and
proprietary artifacts remain in the private lab workspace; this document records
implementation-neutral conclusions and work status.

## Definition of parity

A feature is not considered understood merely because its command and request
fields are known. Its parity dossier must cover all of the following layers:

1. **Request model:** command, field order, wire types, defaults, omission/null
   rules, bounds, and identifier allocation.
2. **Response model:** status predicates, partial success, optional fields,
   collection encodings, malformed input, and pagination.
3. **Orchestration:** every caller and completion block, ordering, cancellation,
   concurrency gates, retries, and terminal decisions.
4. **Persistence:** database reads, insert/update/delete behavior, transactions,
   cursors, migrations, and clean/interrupted lifecycle state.
5. **Consumers:** event dispatch, UI/model updates, deduplication,
   acknowledgement, notification, and downstream follow-up requests.
6. **Failure behavior:** no-progress detection, stale callbacks, disconnects,
   server changes, kickout/revocation, and ambiguous mutations.
7. **Conformance tests:** synthetic success, optional/empty, partial, malformed,
   duplicate, ordering, restart, and failure fixtures derived from the reviewed
   branches.

Only server-selected facts that the binary cannot answer—current acceptance,
retention, rollout, policy, and the encoding chosen on a particular response—go
to a bounded owned-account probe. A live discrepancy becomes a regression test
and triggers a deeper pass through the relevant official caller and storage path.

## Offline workflow

For each operation:

1. Start from the user-visible operation and locate its coordinator method.
2. Generate a private parity report for the coordinator, request sender,
   response handler, and downstream state handler.
3. Resolve Objective-C selector stubs and callback block implementations; do not
   stop at the request constructor.
4. Trace every persistent read/write and distinguish full replacement, merge,
   deletion, and cursor advancement.
5. Record observed facts, hypotheses, and implementation decisions separately.
6. Write or update an implementation-neutral public specification.
7. Add synthetic tests for every relevant branch before changing production
   code.
8. Use at most one preplanned live confirmation for each remaining server-only
   question.

The private analysis helper accepts exact Objective-C selectors and emits method
implementations, direct callers, resolved invoked selectors, direct callees,
referenced strings, and decompilation into the external lab directory. Reports
are version-specific evidence, not files to copy into the implementation.

## Continuity pilot

The first full-chain pilot covers `LOGINLIST`/`LCHATLIST` and `SYNCMSG`.

The official login-list response handler:

- accepts successful and partially successful response objects for processing;
- enumerates `delChatIds` separately from `chatDatas`;
- passes each returned chat record through a stateful chat-data handler rather
  than replacing the database with the response page;
- gates global token and blind-token updates on end-of-list state; and
- may issue secondary information requests after processing returned chats.

Full coordinator tracing further distinguishes page data from durable global
progress. Accepted `LOGINLIST -305` processes its included delta but skips
follow-on list pagination and token commitment. `LCHATLIST -310` processes the
chat/deletion delta, terminates pagination without error, and does not advance the
global token/blind-token cursors. Only status-zero EOF makes those cursors durable.

The chat-data handler constructs or updates the chat room, distinguishes the
last embedded chat log from the last server log, consults the existing message
store, creates explicit loss marks when local history is absent, and collects
chat IDs whose metadata revisions require later synchronization.

The official SYNCMSG chain constructs the reviewed four-field request, sends it
through the carriage manager, processes a nil-safe chat-log collection, and runs
post-sync thread/member follow-up work. Failure to reach the known server
boundary remains explicit state rather than an acknowledged cursor.

The official persistence path represents that state with synthetic loss-mark
messages. It suppresses redundant markers using exact and neighboring-message
lookups, removes an exact marker after boundary recovery, and can delete all
markers in a recovered range after clamping the lower bound to the room's minimum
retained log ID. The clean-room client now preserves the same durable invariant
as a per-chat inclusive interval, kept separate from observed targets and
application commits. This is an implementation-neutral model, not a reproduction
of the official database or UI object.

These findings explain both live regressions found during the pilot: delta chat
pages must merge into durable inventory, and a missing/null `chatLogs` collection
is an empty successful page rather than malformed BSON.

## Current parity matrix

`Mapped` means the complete seven-layer dossier is supported by first-party
evidence and conformance tests. `Partial` means usable behavior exists but at
least one official branch or storage effect remains unresolved.

| Surface | Request/response | Orchestration | Persistence | Consumers/failures | Tests | Status |
| --- | --- | --- | --- | --- | --- | --- |
| QR secondary-device registration | Substantial | Partial | Partial | Partial | Substantial | Partial |
| Booking/check-in/secure carriage | Substantial | Partial | Endpoint cache partial | Partial | Substantial | Partial |
| LOGINLIST/LCHATLIST | Substantial | Pagination and partial-success traced | Delta/global cursor split implemented; official DB model partial | Metadata/link follow-ups open | Strong synthetic coverage | Partial |
| Inbound message events | Common text/reply/photo mapped | Basic dispatch mapped | Explicit durable commit implemented | Official receipt/read-state behavior open | Strong for implemented types | Partial |
| SYNCMSG continuity | Core schema mapped | Recovery and marker callbacks traced | Durable gap lifecycle implemented; official marker placement partial | Retention/bootstrap and post-sync follow-ups open | Paging, persistence, migration, deletion, no-progress, and live regressions covered | Partial |
| Text send | Baseline mapped | No-retry behavior mapped | Message-ID lifecycle partial | Ambiguous delivery modeled | Strong baseline | Partial |
| Photo transfer | Baseline mapped | Multi-stage flow mapped | Resume state partial | Ambiguous stage failures covered | Strong baseline | Partial |
| Replies and reactions | Implemented subset mapped | Primary paths mapped | Revision/storage behavior partial | Some aggregate/detail paths mapped | Implemented subset covered | Partial |
| Membership and chat changes | Inventory only | Missing | Missing | Missing | Minimal | Missing |
| Read receipts, typing, deletion | Inventory only | Missing | Missing | Missing | Minimal | Missing |
| CHANGESVR/KICKOUT/reconnect | Commands and some reasons mapped | Reducer exists | Reset/invalidation partial | Automatic lifecycle not wired | Reducer coverage | Partial |

## Immediate work queue

1. Close the continuity dossier: exact marker-boundary inputs, initial-history
   bootstrap policy, metadata/link follow-ups, and post-sync thread/member work.
2. Trace inbound acknowledgement/read-state behavior from packet handler through
   database mutation and outgoing commands.
3. Trace membership/chat-change response handlers and persistence before adding
   more feature APIs.
4. Revisit text and photo sending for official message-ID persistence,
   cancellation, and restart behavior.
5. Complete reconnect/server-change/kickout orchestration before bridge ownership
   is treated as production-ready.

The matrix is deliberately conservative. Live success demonstrates
interoperability for one path; it does not by itself establish official-client
parity.
