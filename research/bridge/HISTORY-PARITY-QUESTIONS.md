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
cases, with no network entitlement or shared application groups. The isolated process was killed afterward.
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

### Owned observations (2026-10-13)

The [observed window fixture](../fixtures/history-startup/observed-window.json)
records HST-LIVE-001 through 003. Each request selected the same existing
regular group, after full login-inventory visibility and an exact roster of
the three owned disposable accounts were verified. The existing secondary
profile was exclusively leased; no new device was enrolled. A private lab copy
of the clean-room transport exposed raw request/response bodies without adding
an unused API to production. Raw account data stayed outside the repository.

**HST-LIVE-001 — unavailable old interval.** Two `SYNCMSG` requests used
`cnt=0` and an upper bound covering synthetic texts seeded on 2026-10-10.
One started at zero; the other started immediately before the first selected
text. Both returned status 0, `isOK=true`, and `chatLogs=null`. The returned
`minLogId` exceeded the requested maximum in both. Only the zero-cur response
included a positive `jsi`, below the requested maximum. This demonstrates an
unavailable interval below a supplied floor and separates `jsi` from that
floor. It does not determine a fixed retention duration or a join policy.

**HST-LIVE-002 — fresh control and bulk comparison.** One native synthetic
text was sent once in that group. The sender's unread count was 2 before the
probe and stayed 2 after login/inventory/roster verification. A bounded
`SYNCMSG(cur=0, max=freshText, cnt=0)` returned status 0, `isOK=false` and one
type-24 log, without the fresh text. The sender still showed 2 unread.
One-room `MCHATLOGS(chatIds=[selected], sinces=[0])` then returned status 0,
`eof=true`, and five logs of types 24, 14, 24, 14, 1, including the fresh text.
The sender still showed 2 unread after that response.

**HST-LIVE-003 — bounded forward continuation.** A separate bounded request
started at the first SYNCMSG page's returned log ID, with the same fresh upper
bound and `cnt=0`. It returned the remaining four logs, including the fresh
text. The combined two-page log-ID sequence equalled the single bulk page's
sequence, in order and without overlap. Sender unread remained 2.
This final page still had `isOK=false`: that field alone is not an EOF or
more-data flag. The requested upper identity was reached, so no further page
was requested. The static coordinator may attempt continuation and then stop
at its synchronized-state dispatch guard.

Confidence is high for these specific packets, identity/order comparisons and
native unread observations. It establishes a five-log available window and
one group's bounded unread behavior, not oldest/newest selection in a window
larger than 300 at that stage, general direct-chat read policy, fresh-device access or
pre-join visibility. The two receiver emulators remained stopped. The probes
emitted neither `NOTIREAD` nor `CHATONROOM`, committed no history, and retried
no request. Every session exited and released its lease; the sender emulator
was returned to HOME and stopped, and transient UI captures were removed.

**HST-LIVE-004 — direct-chat fetch and unread.** A fresh native text in the
existing direct chat between two owned accounts showed one unread participant.
The count stayed one after secondary login/inventory/roster verification,
a zero-held `SYNCMSG`, and `MCHATLOGS(since=0)`. The single-room request
returned one type-24 log without the fresh text; bulk returned seven ascending
logs of types 24, 24, 24, 14, 14, 14, 1, including the fresh text. The six older
logs carry 2026-10-11 timestamps. Both receiver primary clients stayed stopped;
no `CHATONROOM`, `NOTIREAD` or history commits occurred. The response supplied
positive `jsi` below positive `minLogId`, as in the group observation.
This adds direct-chat evidence, not a guarantee across all rooms or a general
retention duration. A preflight stopped while the exact native draft remained
unsent; another stopped on its trailing newline. Neither issued history.
The draft was completed once after checking it remained in the composer, and
the successful probe verified the normalized unique synthetic preview and
exact owned roster before requesting history.

**HST-STATIC-007 — database write boundary.** Additional Mac 26.8.0 analysis
traces the data-store write wrapper: it runs the supplied block, then processes
changed objects. Changed-object processing takes a lock, begins a database
transaction, saves/deletes the changed objects, commits, clears the change set,
and schedules change notifications. Its exception path rolls back and reverts
affected objects. The reviewed routine does not branch on the transaction
begin/commit return values. This establishes a transaction boundary for one
change-set flush; it does not establish one atomic operation covering history
insertion, later feed reconciliation and bulk pending-set retirement. Cursor
state is persistent, while the pending bulk selection is rebuilt at login.
Crash behavior between these phases and database-error handling remain gaps.

**HST-LIVE-005 — available window larger than 300.** After exact owned A/B
roster verification, 305 distinct numbered synthetic texts were sent once
from the existing secondary profile. Sends were serial, with a two-second pause
after each successful acknowledgement; neither writes nor history requests
were retried. The receiver primary remained outside the chat. Five bounded
`SYNCMSG(cnt=0)` requests returned **1, 80, 80, 80, 71** logs, respectively.
They covered all 305 numbered texts exactly once, in order, plus the seven
older logs already observed. All five responses had `isOK=false`, including
the page that reached the selected upper identity. One initial
`MCHATLOGS(since=0)` returned the **oldest 80 available logs**, with `eof=false`.

**HST-LIVE-006 — bulk continuation and identity comparison.** Three bounded
bulk requests, each starting at the previous bulk page's last returned log ID,
returned **80, 80, 72** logs. Only the final page had `eof=true`; it reached the
selected upper identity. Offline comparison of the retained packets proved
that the combined four bulk pages and five SYNC pages had exactly the same
**312 log identities, in the same ascending order and without overlap**.
The [compact observed fixture](../fixtures/history-startup/observed-large-window.json)
retains these relationships using synthetic identity indices.

This answers B2 for the observed direct-chat window: retrieval progresses
forward through available history rather than selecting the newest 300.
The observed ordinary pages contain 80 logs; the Mac's 300 held-message count
is a separate parameter. Do not generalize 80 to every chat type, payload size
or server version. The first zero-cur SYNC response's single-log boundary also
prevents treating every response as a full ordinary page. No sparse-ID
subtraction, reverse page or server-side newest-N selector was demonstrated.
Own authored writes make this a pagination experiment, not a fresh unread
experiment. No history was committed and no explicit read acknowledgement was
sent. All profile sessions exited and released their leases.

**HST-LIVE-007 — fresh local official-profile attempt.** A separate sandbox
ran an ad-hoc signed copy of the official Mac 26.8.0 build 2000 binary, with
network access and without shared application groups. The original official
client stayed stopped. The owned C
Android 26.8.2 primary accepted the QR flow and displayed successful login
after verification. The isolated Mac then displayed a server-connection error.
Hooks on the bulk and single-room history coordinators and models recorded
**no invocation of those traced history paths** before that failure. This is a failed startup
observation; it does not confirm fresh-profile history, fresh device visibility,
render timing, or official-client crash/resume parity. The altered signing and
sandbox environment are additional limits. The initial experiment stopped after
the completed verification/server-connection failure; the later guarded
diagnostic is below.

**HST-LIVE-012 — guarded diagnostic approval, 2026-10-13 PDT.** A later
controlled attempt used the same isolated Mac 26.8.0 build 2000 environment and
an owned disposable primary. The current QR and actual picker thumbnail were
decoded and compared before the single approval action. The phone explicitly
reported that secondary-device login was restricted; no verification or
completed approval followed. The Mac recorded no auto-login dispatch or traced
history call. The attempt stopped without an automatic retry, restored temporary
phone permissions, removed transient QR/picker captures, and verified the two
mandatory original Mac preference hashes were unchanged.

This establishes the **current approval-policy block**, not the cause of the
earlier completed-verification Mac failure. The diagnostic's Android version was
not independently reverified (the prior lab inventory was 26.8.2). A static
trace narrows the earlier retained wording to the common connection-error
resource, which is used by both an auto-login failure callback and a separate
alert path; the earlier capture did not record which path ran or its error.
Canonical signed-client startup, old-group opening, scrolling and native read
effects remain unverified. No cloud backup or restore was attempted.

The earlier HST-LIVE-007 attempt exposed lab-tool defects: a cached picker
thumbnail retained an expired QR after its file was replaced; a unique immutable image and comparison
of the actual picker QR against the current QR protected the later scans.
The phone's direct `Log In` approval path can lead either to success or a code
form, and that form exposes `Enter the verification code.` as its text when
empty. The lab helper now handles these states, with failing-first synthetic
regressions and a successful observed verification submission. Verification
codes were ephemeral, passed through private stdin, and excluded from files
and published evidence. The earlier incomplete verification was never
submitted; it expired while the helper defects were diagnosed.

All capture hooks were detached and the isolated client stopped. Transient
QR files, picker captures and phone media entries were removed; temporary
photo/camera permission changes were restored, and owned emulators stopped.
Private raw research remains outside the repository.

No history-production bug was exposed: empty-page decoding and reporting an unreached
history target as an explicit gap already have regression coverage. The raw
probes isolate protocol evidence from bridge delivery policy.

Transfer review: Codex, 2026-10-13. Source classes are binary-analysis,
isolated synthetic model execution and controlled owned-account observations.
Redaction and expression review passed. Public fixtures retain only synthetic
identities, protocol field/type behavior, relative boundaries, counts and
original descriptions; raw packets, account data and UI captures are excluded.

**HST-EXEC-002 — startup predicate.** The isolated, signed Mac 26.8.0
no-network/no-shared-group harness executed the real `needsMChatLogs:`
method with synthetic pending-set inputs. Eight cases confirm nil → true,
empty → false, and populated → exact membership. The child was killed.
This executes the predicate only;
it does not execute storage or the complete startup coordinator.

**Provenance correction (final audit).** The harness's preference-hash check
searched the global preferences directory and matched no files; it did not
cover the original client's sandbox preferences. Earlier statements that it
proved unchanged original preferences were unsupported and are removed from
this document and the executed fixtures. Separate bundle/sandbox identity,
signing checks, absent shared groups, and process cleanup were verified. No
whole-profile non-mutation guarantee follows from the empty hash check.

**HST-EXEC-003 — repeat with a fail-closed preference baseline.** The 15
model cases and eight predicate cases were repeated offline with mandatory
baselines for the original sandbox's `com.kakao.KakaoTalkMac.plist` and
`com.google.gmp.measurement.monitor.plist`. Both files existed and their hashes
matched before and after each isolated run; the case outputs matched the
initial runs exactly. A regression rejects an empty or incomplete baseline
and detects a changed file. This verifies those two preference files during
the repeat runs only. It does not retroactively verify the earlier runs or
fresh live attempt, and is not a whole-profile non-mutation guarantee.
The repeated models retained the no-network/no-shared-group isolation, and
both child processes were killed.

## Answers by question family

This index distinguishes an answered client contract from remaining server or
runtime evidence. The detailed static dossier and evidence entries above are
the authority; a linked result does not imply every question in its family is
closed.

| Family | Answer established | Remaining measurement |
| --- | --- | --- |
| A: startup | Eligible rooms use the independent bulk chain; no unread/recency gate; numeric ordering; 100-room batches; persisted since; serial continuation. Fresh stored cursors start at zero, restart reuses them. | Fresh official Mac end-to-end startup, display timing and overlap with unrelated requests. |
| B: opening | Local-first 50-message display, CHATONROOM then guarded sync from stored lastSync; cnt is held count. SYNC continuation depends on isOK, held count and nonempty output. | Group/payload-general window behavior, exact fresh-open observation and server meaning of jsi. |
| C: older/end | Immediate regular timeline scrolling pages the local database backward in groups of 50. Bulk eof retires rooms; target-reaching SYNC can still say isOK=false. Floor is global server minLogId. | Non-restore marker bindings, status-specific server refusals and remote exhaustion UI. Cloud restore remains excluded. |
| D: visibility/content | Below-floor intervals can return status-zero/null logs. Direct and group windows contain ordered earlier logs plus current text. | Retention duration, registration/join visibility, format-general revision behavior and true expired-media observation. |
| E: side traffic | GETMSGS can recover unresolved modified targets; threads reconcile locally; missing room members use MEMBER. Reactions use a separate HTTP metadata chain. | Full author/link/emoticon/thumbnail view-trigger accounting. |
| F: reads | Local divider uses saved seen/own watermark; historical DB badge updates differ from live notification enqueue. Zero-held and bulk fetch left fresh text unread in one direct chat and one group. | Broader no-read guarantee and indirect notification completeness. |
| G: pacing | Main bulk chain is serial and allows at most 100 room IDs/request; each chat controller has its own in-flight guard. Shared carriage disconnect/timeout completes pending callbacks once. | Global overlap cap, maximum real startup burst and history throttle statuses; no deliberate throttle/load experiment. |
| H: persistence | Bulk, sync and seen cursors are distinct; writes flush changed objects in a database transaction. Login rebuilds bulk selection from persisted cursors. | Abrupt interruption across callback phases and database-error return handling. |

### Recovered side-request and media contracts

The existing official-client dossiers resolve several parts of D4/E:

- [Reaction metadata](GROUP-REACTIONS.md#official-client-contract-macos-2680-static)
  is absent from LOCO history aggregates. Open-chat metadata recovery uses
  `/messaging/chats/{chatId}/chat-log/meta/sync-meta?cur&max&cnt`, its own
  revision cursors, a held-meta cap of 300, `last` continuation and repeated-
  parameter guard. The first open-chat timer tick is 10 seconds; later ticks
  depend on successful message sync and differing cursors. HTTP failure retains
  cursors without an automatic retry in that chain. Visible mini reactions
  can cause a separate debounced `/emoticon/chat/rx/logmetas` fetch; reaction
  member/detail requests are on demand. This is static client behavior, not
  historical-server completeness.
- [Photo expiry](GROUP-PHOTOS.md#download) uses epoch-millisecond `expire`,
  local-cache presence and server expiry-class errors. A past expiry does not
  make an already cached original unavailable. The bubble displays an expired
  icon and an original-expired alert when the resource is unavailable. Trailer
  download has its own cache/GETTRAILER/DOWN chain and failure outcomes.
  These are resource/view contracts; history insertion alone does not prove
  a download. A genuinely expired historical photo has not been measured.
- [Historical edits and deletions](GROUP-EDITS-DELETIONS.md#observed-fixtures)
  already have an owned 2026-10-10 observation: SYNCMSG returned edited text
  at its original log position with revision 1 **and** a later feed-25 record.
  It returned a deleted text as type 16385 (text with the deleted flag), still
  containing its original text, **and** the later feed-14 record. Therefore
  the answer for that regular-group case is revised content plus feeds, not
  original-only logs and not feeds-only logs. Never display retained deleted
  content. This evidence is the existing
  [observed fixture](../fixtures/edits/observed-group-edit-delete.json), not a
  newly repeated mutation. It does not establish every format's server policy.
- [Read acknowledgements](../read-state-notiread.md) have a single recovered
  NOTIREAD send caller in live MSG handling. Inventory, catch-up and history
  callbacks do not call it. Explicit reading uses SYNCMSG instead. Sender
  unread observations remain essential because absence of NOTIREAD alone
  does not establish absence of read effects.

## Context

At the request's initial baseline, a fresh login showed no chat history.
At that baseline catch-up covered only chats this profile had already committed
(`ResumeTargets`), regular groups got empty rooms, and direct chats got no room
until a new message arrived. PR #338 now seeds inventory last messages into
portals. The separate operator history path is `history-group` (see
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
  held-message count cap is 300; a successful empty page may omit or null `chatLogs`
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

1. B2 now has a bounded direct-chat result: forward retrieval through the
   available window. Investigate group/payload variations and D1 separately;
   no general server-side "last N" selector is established.
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
