# Membership and chat-change inventory

Status: first-party decoder identity contracts plus bounded lifecycle evidence,
2026-09-30. This note authorizes only the typed identity decoders described below;
it forbids member persistence mutation, UI effects, retries, and reconnects.

## Inventory

The official macOS client contains unsolicited push-notice models for member
and chat changes, including `DELMEM`, `NEWMEM`, `LEFT`, and chat-status/meta
changes. It also contains request/response paths for operations such as
`ADDMEM`, `GETMEM`, and `CHECKJOIN`. Presence in the client is not evidence
that a command's full parity contract is understood.

## Highest-value candidate: DELMEM

`DELMEM` has a dedicated unsolicited model with one model-owned field,
`chatLog`, whose type is a chat-log model. The relevant nested model metadata is
implementation-neutral but exact in type: `chatLog.chatId` and `chatLog.logId`
are signed int64 values; its feed contains a `leaver` member; and that member
contains signed int64 `userId`, signed int32 `userType`, and an optional string
nickname. The proven identity path for a deletion event is therefore
`chatLog.feed.leaver.userId`, paired with the chat and log IDs. Other chat-log
fields include a signed int32 raw/type value and optional message,
attachment, supplement, and extra data, but they are not needed to identify
the departed member.

The handler passes the notice to a manager delegate before any state consumer.
The traced manager callback schedules database-context work rather than
performing a network retry or an automatic reconnect.

Static evidence also shows a membership-oriented database consumer that builds
sets of user IDs, subtracts invitee IDs, compares the current user's ID and
member type, and removes a member by user ID. A separate omitted-history repair
path looks up a room by chat ID, enumerates stored members, and calls the
member-removal operation for selected IDs. These observations establish that
membership persistence exists, and the decoder identity path now matches the
member-removal input. The same evidence does not prove that the consumer is
used identically for every direct-chat, group-chat, or open-chat shape.

## NEWMEM bounded identity trace

`NEWMEM` has the same one-field unsolicited model shape: a `chatLog` object.
The model metadata gives `chatLog.chatId` and `chatLog.logId` as signed int64
values. Its feed exposes an `invitees` collection typed as a member-array
model; each member has signed int64 `userId`, signed int32 `userType`, and an
optional string nickname. The handler delegates the notice to the manager.

The manager callback schedules database work and consumes `chatLog.feed` and
`feed.invitees`. Static calls then use `chatLog.chatId` to look up a chat room,
conditionally request chat information, and pass a copied invitee collection
through a member-update operation. A separate block turns the chat log into a
chat-message/database operation. This establishes the wire identity path and
the persistence ownership boundary, but not the full state contract.

## CHGCHATST bounded status trace

The `CHGCHATST` unsolicited model has signed int64 `chatId`, signed int64
`plusUserId`, signed int64 `revision`, and a `chatStatus` dictionary. Static
evidence does not establish names, types, or semantic labels for dictionary
entries or enumerate a stable status-value set, so the decoder handoff keeps
that dictionary opaque.

The handler delegates the notice to the manager. The manager looks up the room
by chat ID and only schedules database work when the room and status dictionary
are present and the notice revision is newer than the room's stored status
revision. The database block merges the status dictionary into room extra
metadata under the observed status/revision keys. A downstream delegate event
is emitted, and a separate UI/client path can request a fresh status by chat
ID. No network retry or status-value interpretation is proven here.

The narrow gate is strict: a missing room, missing status dictionary, stale
revision, or equal revision skips the database block and downstream delegate.
For a newer revision on an existing room, the block preserves existing extra
metadata, writes the raw status dictionary under `cs`, writes the incoming
revision as an int64 under `csr`, and only then emits the manager-owned
delegate event. The traced path uses a synchronous database-block call with
no explicit completion/error branch, so write-failure reporting and rollback
remain unproven. No chat-type guard is visible in this gate; any separate UI
refresh guard is not conflated with persistence.

## CHGMETA bounded metadata trace

The `CHGMETA` unsolicited model carries signed int64 `chatId` and a nested
`meta` object. The nested model exposes signed int32 `type`, signed int64
`revision`, signed int64 `authorId`, string `content`, and signed int64
`updatedAt`. The numeric subtype values observed in the manager block include
3, 14, 15, and 21; their semantic labels are intentionally not assigned here.

The handler delegates the notice to the manager. The manager schedules a
database block for the chat room. Static control flow branches on the metadata
subtype and performs revision/content comparisons before invoking downstream
room or calendar consumers; one helper requests a typed metadata refresh using
the chat ID. Exact persistence keys, optional fields, subtype meanings,
chat-type guards, and failure reporting remain open, so the decoder preserves
the raw metadata fields without implementing those effects.

## CHGMCMETA bounded metadata trace

`CHGMCMETA` is distinct from `CHGMETA`. Its unsolicited model carries signed
int64 `chatId`, signed int32 `revision`, and four string fields: `type`,
`content`, `imageUrl`, and `fullImageUrl`. The string values are preserved as
opaque metadata; no stable semantic labels are assigned to `type`.

The carriage handler delegates the notice to the manager. The manager queues
database-context work, looks up the room by `chatId`, and applies the notice
only when that room exists. The room consumer routes on the opaque `type`
value and reads the content or image URL fields; the traced manager path also
compares and advances a separate MCM revision and can update pin/folder state.
Optional string-field behavior, exact type values, database failure reporting,
chat-type guards, and downstream notification semantics remain open. The
decoder therefore exposes only the six model fields and performs no room or
revision mutation.

## Explicit gaps

The following layers remain unproven for `DELMEM`; this slice adds only a
typed decoder identity contract and no member mutation:

- optional/null behavior when `chatLog`, `feed`, or `leaver` is absent;
- status/error interpretation and database transaction failure behavior;
- direct-chat versus group-chat guards and local event/UI consumers; and
- malformed-body behavior of the official model initializer;
- optional/null behavior when `NEWMEM.feed` or `feed.invitees` is absent; and
- the source and semantics of the manager's link/cursor argument and its
  completion callbacks.

The clean-room decoder now exposes typed boundaries for `DELMEM`, `NEWMEM`,
`LEFT`, `CHGCHATST`, `CHGMETA`, and `CHGMCMETA`. Each typed event has a
synthetic decoder test for
the proven field path, while stateful lifecycle remains
unimplemented. The decoder fails closed for missing
or wrong nested structure as an implementation safety rule; that behavior is
not claimed as an observation of the official malformed-body path. This is not evidence that the
official client ignores any of these methods.

The NEWMEM decoder applies the same fail-closed safety rule to missing or
wrong invitee structure; its synthetic malformed tests do not claim official
malformed-input equivalence.

The CHGCHATST gaps are optional/null and malformed-body behavior, database
failure reporting, the exact chat-type guard for downstream status refresh,
and the semantic status-value set. Its synthetic test therefore asserts only
the four proven top-level fields and leaves `chatStatus` opaque.

The pure reducer characterization in
`internal/protocol/events/chgchatst_transition_test.go` encodes only the
proven strict revision gate and `cs`/`csr` merge. It does not claim database
transaction semantics, UI behavior, or delegate delivery on an I/O failure.

## LEFT bounded identity and ownership trace

The `LEFT` unsolicited model has two own fields: signed int64 `chatId` and
signed int64 `lastTokenId`. It has no member identity or `chatLog` field, so the
wire model describes the local client's chat departure rather than another
member leaving. The carriage handler reads `lastTokenId`, updates the manager's
cursor, and then delegates the notice to the manager.

The manager schedules a database block that looks up the room by `chatId` and
calls the room deletion operation. It also updates the manager cursor and
invokes a calendar synchronization path for a leave-team-chat case. The
observed block has no explicit completion callback or network retry. This is a
typed decoder boundary only: room deletion, cursor mutation, and calendar
effects remain manager-owned.

The following LEFT details remain open: missing/null field behavior, database
failure reporting, exact chat-type guard for the calendar path, and downstream
UI/notification behavior. The synthetic decoder test therefore carries only
the two proven int64 fields and does not assert deletion or calendar effects.
Its malformed-input tests are fail-closed implementation safety checks, not
claims about official malformed-input equivalence.

## Evidence trail

- Client: official macOS KakaoTalk 26.8.0 arm64, inspected 2026-09-30.
- Method: read-only Ghidra model metadata, selector/caller reports, and
  decompilation of the DELMEM packet handler and database callback; no live
  membership mutation was performed.
- Public transfer: only field names/types, ownership boundaries, and synthetic
  decoder identity behavior are recorded here. Private binary names, offsets,
  decompiler output, accounts, and message data are omitted.
