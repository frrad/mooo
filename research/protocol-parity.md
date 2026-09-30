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

The shared carriage callback lifecycle is now traced separately from command
semantics. Request completions are registered in memory under packet
correlation identities, removed on matching response, and fanned out with an
error exactly once on socket disconnect before both pending maps are cleared.
Receive timeout and explicit disconnect converge on that socket teardown; the
carriage agent does not retry or reconnect. Its packet IDs occupy the bounded
range 100000000 through 199999999 before wrapping. CHANGESVR and KICKOUT are
delegated to the owning manager for route clearing/logout/reset decisions.
See [`research/carriage-callback-lifecycle.md`](carriage-callback-lifecycle.md).

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
through the carriage manager, and processes a nil-safe chat-log collection.
`doAfterSyncMsg:` invokes chat-thread reconciliation only when the room
accumulated pending thread IDs, then clears that set. The invoked Swift wrapper
converts the chat-to-thread-ID mapping, runs local database work, revises stored
thread state from room/message data, and broadcasts an internal completion
signal; no LOCO thread-fetch request was observed. A nearby member-add request
initially appeared inside the same decompiler function, but the Objective-C
method table places it in the separate
`doAddMemWithChatRoom:memberIds:completion:` method; it is not a SYNCMSG
member-refresh branch. Failure to reach the known server boundary remains
explicit state rather than an acknowledged cursor.

The official persistence path represents that state with synthetic loss-mark
messages. It suppresses redundant markers using exact and neighboring-message
lookups, removes an exact marker after boundary recovery, and can delete markers
strictly inside a recovered range after clamping its lower bound to the room's
minimum retained log ID. LOGINLIST marker creation uses either `prevId + 1` from
the returned last chat log or `lastServerLogId + 1` when no last chat log exists.
SYNCMSG uses the first returned message's `prevId + 1` after checking its `jsi`
boundary and local storage; it clears the marker at `max + 1` when the next known
server message links back to the recovered maximum. The clean-room client preserves
the same durable invariant as a per-chat inclusive interval, kept separate from
observed targets and application commits. This is an implementation-neutral
model, not a reproduction of the official database or UI object.

A similarly named `lastLogId + 1` marker helper is not part of the observed
ordinary-login chain. Its sole resolved caller is cloud-restore new-count
recomputation, where it repairs a missing restored boundary while suppressing an
existing exact message or marker. The normal startup policy is the reviewed
`LOGINLIST`/`LCHATLIST` chat-data path followed by bounded history recovery; the
restore helper is not a general bootstrap rule. Kakao cloud backup/restore is
outside this project's product scope; this path is retained only as negative
evidence against applying restore behavior to normal sessions.

The chat-list metadata follow-up is now traced end to end. Positive OpenChat link
IDs with no stored link are deferred; missing links and stale stored link tokens
are batched into `INFOLINK`. The request carries an int64 ID array. On success,
the returned `openLinks` collection is updated in the database before the client
replays the deferred chat-data records. A failed response does not replay them.
Non-OpenChat records have no dependency on this request. This closes the
previously open identification and ordering question without making direct-chat
resume contingent on OpenChat metadata support.

These findings explain both live regressions found during the pilot: delta chat
pages must merge into durable inventory, and a missing/null `chatLogs` collection
is an empty successful page rather than malformed BSON.

## Read-state pilot

The first-party inbound read-state command is `DECUNREAD`. Its model contains
int64 `chatId`, `userId`, and `watermark` fields. The packet handler delegates the
notice into the database context. For every member, it advances that room
member's watermark. When `userId` is the current account, it also recomputes or
clears the room's unread count against the new watermark, clears mention/reply
state when the watermark reaches the last log, and updates joined/archive state.

`NOTIREAD` is a distinct automatic response in the official inbound-message
path, not sufficient evidence of an explicit user mark-read action. After an
accepted `MSG` callback and an existing room lookup, the official client sends
`NOTIREAD` with int64 chat ID, link ID, and message-log watermark, plus the
room's boolean notification-read value and the message service ID. The clean-room
client does not yet emit it: acknowledgement semantics, failure behavior, and
the explicit `CHATONROOM` mark-read lifecycle remain under review. Static tracing
of the Mac client's explicit `markAsRead` and read-all entry points routes those
operations through `SYNCMSG`; no separate mark-read LOCO request was found.
`CHATOFF` in this path is local room teardown, and no distinct `CHATOFF` wire
request was found. These names therefore must not be exposed as independent
server operations without further evidence.

A bounded owned A/B experiment on 2026-09-30 found that LOGINLIST alone left the
sender's unread marker in place, while a status-zero `SYNCMSG` crossing the
one-message boundary removed it. Receiving a live `MSG` while connected also
appeared to clear the sender's unread marker even though the clean-room client
did not emit `NOTIREAD`; the mechanism and official-client path remain
unresolved. `SYNCMSG` is consequently not semantically read-only: catch-up can
have read side effects. Until the acknowledgement and failure contract is
traced, the clean-room client must keep inbound delivery, application commit,
and explicit read state distinct rather than inferring read state from receipt.
The request initializer uses the short `li` property for link ID and writes all
five fields before dispatch through the shared carriage request path. The
response callback forwards a non-null packet as a response object without a
status branch; carriage-unavailable sends fail before a packet, and receive
timeouts disconnect the agent. No NOTIREAD-specific persistence consumer was
found, while pending-callback fan-out after disconnect and the server meaning
of the response boolean remain open. The static chain and synthetic contracts
are recorded in [`research/read-state-notiread.md`](read-state-notiread.md) and
[`research/read-state-notiread-response.md`](read-state-notiread-response.md).

## Current parity matrix

`Mapped` means the complete seven-layer dossier is supported by first-party
evidence and conformance tests. `Partial` means usable behavior exists but at
least one official branch or storage effect remains unresolved.

| Surface | Request/response | Orchestration | Persistence | Consumers/failures | Tests | Status |
| --- | --- | --- | --- | --- | --- | --- |
| QR secondary-device registration | Substantial | Partial | Partial | Partial | Substantial | Partial |
| Booking/check-in/secure carriage | Substantial | Partial | Endpoint cache partial | Partial | Substantial | Partial |
| LOGINLIST/LCHATLIST | Substantial | Pagination, partial success, and OpenChat metadata follow-up traced | Delta/global cursor split implemented; OpenChat link store not implemented | Detailed official room DB/UI model partial | Strong synthetic coverage | Partial |
| Inbound message events | Common text/reply/photo/read-state mapped | Basic dispatch mapped | Explicit durable message commit implemented; SYNCMSG may also affect read state | Live MSG appeared to clear sender unread without an observed NOTIREAD; mechanism unresolved | Strong for implemented types | Partial |
| SYNCMSG continuity | Core schema mapped | Recovery, marker, restore-only boundary repair, and post-sync callback traced | Durable gap lifecycle implemented; principal marker and local-thread transitions mapped | Retention/error boundaries and UI presentation open | Paging, persistence, migration, deletion, no-progress, and live regressions covered | Partial |
| Text send | Baseline mapped | No-retry behavior mapped | Message-ID lifecycle partial | Ambiguous delivery modeled | Strong baseline | Partial |
| Photo transfer | Baseline mapped | Multi-stage flow mapped | Resume state partial | Ambiguous stage failures covered | Strong baseline | Partial |
| Replies and reactions | Implemented subset mapped | Primary paths mapped | Revision/storage behavior partial | Some aggregate/detail paths mapped | Implemented subset covered | Partial |
| Membership and chat changes | Inventory only | Missing | Missing | Missing | Minimal | Missing |
| Read receipts, typing, deletion | DECUNREAD, NOTIREAD, and SYNCMSG read-side effect path mapped | Mac markAsRead/read-all routes through SYNCMSG; CHATOFF is local teardown; no distinct wire requests found | Official DECUNREAD mutations traced; client persists successful SYNCMSG read watermarks separately from message commits | SYNCMSG is not semantically read-only; live NOTIREAD/read-side-effect mechanism and failure behavior remain unresolved | DECUNREAD parser coverage; bounded A/B observation; synthetic SYNCMSG success/failure and persistence coverage | Partial |
| CHANGESVR/KICKOUT/reconnect | Commands and some reasons mapped | Manager delegation, pending-failure fan-out, and packet-ID lifecycle traced | Route clearing/reset ownership mapped; full durable recovery partial | Reconnect remains an explicit manager decision | Reducer plus lifecycle characterization; packet-ID wrap pending | Partial |

## Immediate work queue

1. Complete the read-state dossier: trace the status, acknowledgement, and
   failure behavior around `SYNCMSG`, `DECUNREAD`, and `NOTIREAD`; extend the
   existing synthetic SYNCMSG read-side-effect coverage as new official
   behavior is established before exposing broader read-state APIs.
2. Live-confirm remaining continuity server behavior: cursor inclusivity,
   retention/error boundaries, and `INFOLINK` optional/empty encodings.
3. Trace membership/chat-change response handlers and persistence before adding
   more feature APIs.
4. Revisit text and photo sending for official message-ID persistence,
   cancellation, and restart behavior.
5. Complete reconnect/server-change/kickout orchestration before bridge ownership
   is treated as production-ready.

The matrix is deliberately conservative. Live success demonstrates
interoperability for one path; it does not by itself establish official-client
parity.
