# Fresh-login history: official-client information request

Status: active research, 2026-10-13. Findings are recorded below as they are
established. Inventory last-message seeding landed separately in PR #338;
this PR adds research evidence, not new history implementation.

## Findings so far

The [static dossier](HISTORY-STATIC.md) records HST-STATIC-001 through 006
(Mac 26.8.0, read-only binary analysis). Ordinary startup has a separate
`MCHATLOGS` chain, independent of loss marks: numeric chat-ID ordering, at
most 100 valid rooms per request, persisted `lastMChatLogId` values in
`sinces`, and `eof`-driven continuation. Inventory preview updates preserve
that cursor while startup's pending set is nil. Local timeline display reads
50 messages from storage; this does not establish a server page size.

Two earlier descriptions are corrected: marker range cleanup uses the active
client's server-supplied `minLogId`, not a room-local oldest message; and the
single-room synchronized-state skip requires `lastSyncLogId` to equal both
`lastSeenLogId` and `lastLogId`. SYNCMSG continuation uses `isOK`, request held
count and returned-log count, rather than bulk `eof`.

**HST-EXEC-001** (2026-10-13, Mac 26.8.0 arm64 build 2000): an isolated signed
offline harness ran the official request/response models on 15 synthetic
cases, with no network entitlement or shared application groups. Official
preference hashes were unchanged and the isolated process was killed afterward.
The [executed fixture](../fixtures/history-startup/model-runtime.json) records:

- `MCHATLOGS` serializes `chatIds`/`sinces`, including a zero `since`.
  Empty and mismatched arrays also serialize at the **model** layer; coordinator
  validation and server acceptance do not follow from that behavior.
- Bulk responses retain `eof`; an absent `eof` defaults to false. Empty
  `chatLogs` gives an empty collection; omitted/null gives nil.
- `SYNCMSG` decodes `isOK`, `minLogId` and `jsi`. Absent scalar fields default
  to false/zero. `isOK` and success status are independent: an unsuccessful
  synthetic response can retain `isOK=true`, so callers must gate success first.

Confidence is high for these executed model cases, limited to the exact build
and inputs. This does not execute startup scheduling or cursor persistence and
does not establish server ordering, retention, pre-join visibility or read
effects. Those questions remain open below.

The maintainer lifted the live test-account hold on 2026-10-13. Future owned
observations use bounded requests, private attempt receipts and cleanup; cloud
backup/restore remains excluded. No live experiment preceded this model result.

## Context

At the request's initial baseline, a fresh login showed no chat history.
PR #338 now seeds inventory last messages into portals. Catch-up only covers
chats this profile has already committed (`ResumeTargets`), regular groups get
empty rooms, and direct chats get no room until a new message arrives. The only
history path is the operator command `history-group` (see
[group history](GROUP-HISTORY.md)).

The proposed fix is staged:

1. Create rooms for every chat at login, direct chats included, from the
   `LOGINLIST`/`LCHATLIST` inventory.
2. Deliver each chat's last message, which the inventory already carries.
3. Eagerly load a small recent window for recently active chats.
4. Load older history on demand.
5. Pace history requests.

Steps 1 and 2 need no extra Kakao traffic. Steps 3–5 should follow what the
official client does: when it fetches history, what it requests, and what it
does with the result. This document lists the information required to do that
faithfully, separating what is known from what is not.

## Known

- `SYNCMSG` takes int64 `chatId`, `cur`, `max` and int32 `cnt`; the normal page
  limit is 300; a successful empty page may omit or null `chatLogs`
  (SL-BIN-011, SL-LIVE-011, [message continuity](../message-continuity.md)).
- `cnt` declares how many messages the client already holds after `cur`, not a
  page size; `cnt=0` returns messages the client lacks (SL-BIN-035,
  SL-LIVE-013).
- One owned regular-group differential: `cnt=0` did not visibly mark messages
  read; `cnt=1` did ([group history](GROUP-HISTORY.md)). Not yet repeated for
  direct chats or generalised.
- On `LOGINLIST`, the Mac client can create a loss mark at
  `lastChatLog.prevId + 1`, but only when `prevId` is positive, that exact
  previous message is absent locally, **and older normal history already exists
  locally**. Without a last chat log, a positive `lastServerLogId` with no exact
  stored message can produce a mark at `lastServerLogId + 1` (SL-BIN-015).
- The `SYNCMSG` callback raises `lastSyncLogId` and `lastMChatLogId` to the
  returned maximum, removes loss marks strictly between the returned minimum
  and maximum, can create a mark at `first.prevId + 1` when the previous
  message lies beyond the response's `jsi` boundary, and removes the mark at
  `max + 1` when the next known message has `prevId == max`. Range removal
  clamps its lower bound to the active client's server-supplied `minLogId` (SL-BIN-014,
  SL-BIN-015).
- `MCHATLOGS` uses the same open-range and next-message cleanup pattern
  (static only).
- The `lastLogId + 1` loss-mark helper is reached only from cloud-restore
  recomputation, not ordinary startup (SL-BIN-018).

### Consequence of the loss-mark condition

On a fresh install no older local history exists, so the `LOGINLIST` loss-mark
condition fails. A freshly logged-in official client may therefore hold only
each chat's last message with no marker before it, and load earlier messages
through a different mechanism. That mechanism is now traced statically as
`MCHATLOGS`, as described above. The server's returned window and fresh-install
behavior still need observation.

## Required information

Each item names the step it informs and the method expected to answer it:
**static** (Mac binary trace through caller, callback, persistence, consumers
and failure behavior), **executed** (offline signed harness running the
official model code), or **observed** (controlled owned-account experiment).

### A. History fetched at login without user action (step 3)

The eager bulk request path is now established statically; the returned window
and fresh-install result remain unobserved.

1. Every `SYNCMSG`, `MCHATLOGS` or other history request issued after
   `LOGINLIST`/`LCHATLIST` completes: trigger, selected chats, parameters.
   *Static; observed for confirmation.*
2. `MCHATLOGS`: request model fields, chats per request, messages per chat,
   callers and gating (all chats, unread chats, most recent N, visible rows),
   response model, persistence and failure behavior. Candidate mechanism for
   bulk recent history. *Static, executed.*
3. Unread chats: whether the inventory's unread count or last-seen log ID
   triggers an eager fetch of the unread range, and any cap. *Static.*
4. Ordering and concurrency across chats: recency, chat ID or parallel;
   global request cap; whether the work waits for the chat list to render.
   *Static.*
5. Fresh-install versus restart branches (first-sync flags, per-room
   "loaded once" state). Label each branch; exclude restore-only paths.
   *Static.*

### B. Opening a chat (steps 3–4)

1. Exact request when a never-opened room is opened: `cur` (zero?), `max`
   (last chat log, `lastServerLogId` or `ll`?) and `cnt`. *Static, observed.*
2. **Which end of an interval the server returns.** With `cur=0`, `max` at the
   newest log and thousands of messages, does the server return the oldest 300
   after `cur`, the newest 300 up to `max`, or a server-bounded window? Log IDs
   are sparse, so `max - N` arithmetic is not available. If paging is
   forward-only, "last N messages" needs another mechanism. *Observed.*
3. The full `SYNCMSG` response model, including any end-of-list or more-data
   flag, and the rule for requesting another page versus stopping. *Static,
   executed.*
4. `jsi`: type, meaning (join boundary, retention floor, sync boundary) and
   every consumer. If it is a retention or join floor, it bounds step 4.
   *Static, executed, observed.*
5. Amount loaded on open: one page, fill-the-viewport, prefetch. *Static.*
6. Read effects of opening a chat and their mechanism (`NOTIREAD`
   (SL-BIN-021), a positive-`cnt` request, or explicit mark-read), traced
   separately from the fetch. Bridge backfill must fetch without reading.
   *Static.*

### C. Older history and loss marks (step 4)

1. Path from reaching the top of the timeline or a visible loss mark to a
   request: command, `cur`/`max`/`cnt`, page size, prefetch distance. *Static.*
2. End-of-history detection (empty page, flag, `jsi`, minimum retained log ID)
   and how it is presented. *Static, observed.*
3. Origin of the room's minimum retained log ID: server field or oldest local
   message. A server value is the retention floor. *Static.*
4. State changes from an older page: does anything move backward, and does
   older paging interact with live catch-up cursors? *Static.*
5. Failure behavior per status (left chat, no permission, expired history),
   empty response and timeout: marker kept, retry, backoff. *Static.*

### D. Server retention and visibility (step 4)

1. How far back a newly registered sub-device can read: all history, since
   device registration, a time window or a count. Seed messages of known ages,
   including before registration. *Observed.*
2. Pre-join visibility in groups, and whether `jsi` encodes it. *Observed.*
3. Direct-chat versus group differences. *Observed.*
4. Historical media: expiry presentation, and whether thumbnails are fetched on
   load or on view. *Static, observed.*
5. Edits, deletions, reactions and feeds in historical pages: latest revision
   or original plus later feeds; deleted placeholders; reactions inline or via
   separate sync; membership feed interleaving. *Observed.*

### E. Side requests per history page (steps 3–5)

Profile reads (`MEMBER`) for authors absent from the current roster, reaction
metadata sync, thread reconciliation (local only per SL-BIN-017), link and
emoticon metadata, and media downloads. Needed for traffic accounting and
parity. *Static.*

### F. Read state (steps 3–4)

1. Whether `cnt=0` has no read effect in direct chats and groups generally.
   *Observed.*
2. How loaded history is displayed relative to the inventory's last-seen log
   ID, so the bridge can place the Matrix read marker instead of creating
   unread messages. *Static.*
3. Whether loaded history raises notifications. *Static.*

### G. Pacing (step 5)

1. Serialisation scope of history requests: per room, global, bounded
   parallelism. *Static.*
2. History-specific timeout and retry behavior beyond the shared carriage
   correlation and disconnect fan-out (SL-BIN-024). *Static.*
3. Server throttling statuses for history requests and the official reaction.
   Probe live only with care; this account family has prior login-block
   history. *Static first; observed only if needed.*
4. The largest burst the official client issues, which sets a plausible
   envelope for a new sub-device. *Static, observed.*

### H. Persistence (steps 3–4)

1. Complete per-room history state (`lastLogId`, `lastSyncLogId`,
   `lastMChatLogId`, minimum retained log ID, loss marks) and its updates from
   `LOGINLIST`, `SYNCMSG`, `MCHATLOGS` and live messages. *Static.*
2. Resume behavior after interruption mid-load. *Static.*

## Suggested order

1. Observe B2 and D1 with mooo's own bounded `SYNCMSG` probes on owned
   accounts. Until they are answered it is unknown whether "last N messages"
   is reachable through `SYNCMSG` at all.
2. In parallel, statically trace A and C from the `SYNCMSG` and `MCHATLOGS`
   constructors up to callers and UI triggers and down through callbacks,
   persistence and failure paths, and execute the `MCHATLOGS` model in the
   offline harness.
3. Observe a freshly registered official Mac client on an owned account: chat
   list, each chat on open, scroll-up, with unread state checked from another
   device. This answers the fresh-install question directly.

Record results as evidence entries with version, date, method, sanitized
observation and confidence, and turn request/response shapes into fixtures per
[parity fixtures](../parity-fixtures.md).
