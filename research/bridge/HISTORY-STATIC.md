# Fresh-login history: Mac static findings

Evidence date: 2026-10-13. Reference: KakaoTalk for Mac 26.8.0, arm64.
Method: read-only Ghidra method, selector-call, class-metadata, callback,
disassembly and persistence analysis of the authorized official binary.
This answers the static part of [PR #336](https://github.com/frrad/mooo/pull/336).
No official client was launched, no model harness was executed, and no test
account, emulator, bridge, carriage, QR or network experiment was used.

These are **static findings**, not executed parity or observed server behavior.
Raw reports remain in the private external lab. Public descriptions below are
implementation-neutral; synthetic cases are
[static leads](../fixtures/history-startup/static-control-flow.json).

## Evidence and confidence

| ID | Finding | Confidence and limits |
| --- | --- | --- |
| HST-STATIC-001 | Ordinary post-inventory startup has a bulk `MCHATLOGS` path independent of loss marks. | High for the recovered control flow; server window and fresh-install results unobserved. |
| HST-STATIC-002 | Bulk selection uses persisted per-room cursors, numeric chat-ID ordering, batches of at most 100, and `eof` continuation. | High for constants, branches and callback dependencies; no global concurrency guarantee. |
| HST-STATIC-003 | The loss-marker cleanup floor is the client-wide server-supplied `minLogId`. | High for login/SYNCMSG writes and consumers; its server policy is unknown. |
| HST-STATIC-004 | Chat open first displays local history; older display reads use a 50-message local limit. `SYNCMSG` continuation uses `isOK`, held count and returned logs. | High for traced paths; initial server window, viewport prefetch and UI event coverage remain limited. |
| HST-STATIC-005 | Bulk and single-room callbacks reconcile feeds, cursors and missing-history state; fetching and local read state are intertwined. | High for direct callback effects; server read effects, notification behavior and interruption durability require separate confirmation. |
| HST-STATIC-006 | The notification manager's database observer updates counters/badges; notification delivery has a separate enqueue path. A message-restore precheck reaches cloud backup configuration. | High for direct consumers; other indirect observers and all restore UI bindings are not exhaustively proved. |

## A. Ordinary startup and bulk history

The login operation clears its pending bulk-history selection to **nil** before
processing the inventory. While that selection is nil, the predicate asking
whether a room needs bulk history returns true. Updating an inventory room's
last message therefore does not also advance its bulk-history cursor through
that last-message update path. This protects the distinction between an
inventory preview and completed bulk history.

After the chat-list phase, the client examines all locally known rooms. A room
qualifies when both:

- `lastMChatLogId < lastLogId`;
- `lastLogId >=` the client-wide `minLogId`.

This selection has no unread-count, recency, visible-row or first-open test.
It does not depend on a loss marker or older local messages. The client stores
the qualifying chat IDs as a pending set, initializes its thread-reconciliation
dictionary, and starts bulk fetching. It is an ordinary login/list path, not
the cloud-restore recomputation described by SL-BIN-018.

The pending IDs are sorted by their signed 64-bit numeric value, ascending.
The client selects at most **100 valid rooms** for one request; a missing or
deleted room is removed from the pending set. Each selected room contributes
its chat ID and persisted `lastMChatLogId` to parallel arrays. There is no
client-selected per-room message count in this request model.

The request method is `MCHATLOGS`; its declared fields are `chatIds` and
`sinces`, both arrays of 64-bit integers. The response model declares a typed
`chatLogs` collection and Boolean `eof`, in addition to shared response state.
The subsequent HST-EXEC-001
[model run](HISTORY-PARITY-QUESTIONS.md#findings-so-far) confirms empty versus
omitted/null collections and default-false `eof`; broader coercion and
coordinator behavior remain unexecuted. The `GETMSGS` fixture is separate.

On a successful response, the lower coordinator persists/reconciles its logs
before delivering the response to the bulk scheduler. The scheduler examines
`eof`:

- With `eof=true`, it retires the selected IDs from the pending set.
- With `eof=false`, it takes the chat ID of the response collection's **last
  log** and retires selected IDs strictly below that ID. That chat remains
  eligible, with its updated persisted cursor, for the next request.

Pending-set retirement runs through the database context and its completion
starts the next bulk selection. The main startup bulk chain is consequently
callback-driven and serial. This does not prove that unrelated source requests
or separate chat controllers cannot overlap it.

A nil response ends this scheduling attempt through its completion path.
A nonnil unsuccessful response proceeds to the room-metadata phase rather than
using the successful page-retirement branch. There is no history-specific
sleep, throttle-code switch or retry backoff in the traced scheduler. A
successful empty response with `eof=false` has no established progress proof:
the continuation uses the last returned log's chat ID, not an independent
server cursor. Do not copy this into an unbounded bridge loop.

The list machinery signals chat-list initialization and performs room metadata
work as part of startup. The recovered ordering is protocol/database ordering;
it does not establish when pixels appear or a viewport becomes interactive.

### Fresh installation versus restart

The room storage schema initializes `lastMChatLogId` and `lastSyncLogId` to
zero. Inventory room updates set the last log from the inventory and retain
these as distinct state. Combined with the nil-selection guard, the static
path permits a new room with a positive eligible last log and zero bulk cursor
to issue `MCHATLOGS` with `since=0`. A restart instead uses the stored bulk
cursor and the same eligibility predicate.

This closes the request's initial *control-flow* gap: missing loss marks do
not imply that startup has no other history fetch. It does **not** establish
how many messages the server supplies, which end of a large interval it
chooses, or how far before device registration it permits access. There is
no basis yet for selecting a bridge policy of "last N" or "last message only"
as official fresh-login parity.

## B. Floors and `jsi`

The `minLogId` used for startup eligibility and loss-mark range clamping is
stored on the active protocol client, not on each room. Successful login
processing copies it from the login response. The successful `SYNCMSG`
database callback raises it when the response supplies a larger `minLogId`.
Room inventory and unread-count routines also consume this client-wide floor.

Earlier descriptions calling the cleanup clamp "the room's minimum retained
log ID" were imprecise. The lower cleanup bound is clamped to the active
client's `minLogId`, then the room-specific marker query deletes only entries
strictly inside the resulting interval. This is a server-supplied boundary;
its name and use do not prove a time-based retention period, registration
boundary or join boundary.

`SYNCMSG` separately declares signed 64-bit `jsi` and `minLogId`, a Boolean
`isOK`, and typed `chatLogs`. These boundaries must not be conflated.
The resolved `jsi` selector reads occur in two callback families:

- The database callback records diagnostic values and uses positive `jsi`
  to suppress a missing-predecessor marker when `first.prevId <= jsi`.
  The normal predecessor checks still require positive `prevId`,
  `prevId < first.logId`, and no exact predecessor locally.
- The per-room completion records diagnostic values and compares `jsi`
  with the room's last log. When `jsi <= lastLogId` and the exact last-log
  message is absent, it enters a database marker-creation block.

The meaning of `jsi` on the server remains unclassified. A field inventory
and these consumers cannot establish pre-join visibility or exhaustion.

## C. Chat open, local paging and single-room sync

Opening a regular chat resolves the local room and roster, derives a local
read-divider position, displays locally stored latest messages, then issues
`CHATONROOM`. It does not start by constructing an unconditional
`SYNCMSG(cur=0, max=inventoryLastLog, cnt=0)`.

The latest-message display routine reads the database with a 50-message
limit. Its read-divider branch can choose messages around the saved divider
when sufficient local messages exist; otherwise it loads the latest local
messages. The older-display routine chooses the first displayed message
(skipping a leading read-divider item where needed) and requests **50 local
messages** in the backward direction. Its no-motion checks can defer that
local load. Its immediate fetch chain reaches database queries, not a LOCO
history-page request. The corresponding thread display has a separate path.

Thus neither 50 nor the UI's local "has more messages" predicate establishes
a server page size or remote end-of-history signal. Scrolling cannot retrieve
data absent from the local store through this immediate local-fetch chain.
Loss-marker UI actions and special chat types must be traced separately;
cloud-restore actions are outside this work's scope.

The recovered message-restore precheck reaches cloud backup-configuration
work before starting its continuation. This is a restore-dependent affordance,
not evidence of ordinary `SYNCMSG` older paging. It was inspected only far
enough to prevent a false parity inference; no restore operation was executed
or implemented. Other marker UI bindings and Open/Team Chat mechanisms remain
outside the regular-chat chain established here.

The chat controller's single-room sync has an in-flight flag. It checks
connection/room eligibility and compares the room's last sync position with
its own watermark before entering the per-room protocol coordinator. Explicit
mark-as-read, read-all, summary-and-read, and exact-message retrieval also
reach that coordinator. Its `cur` is persisted `lastSyncLogId`; its `max` and
`cnt` come from the local held-message query (cap 300, fallback to room last
log), as already established by SL-BIN-035. The database query takes the
caller-supplied maximum as another bound.

The coordinator skips dispatch only when **both** `lastSyncLogId ==
lastSeenLogId` and `lastSyncLogId == lastLogId`; a difference in either can
enter synchronization. Deleted rooms and a calculated maximum below the
sync position take completion paths without a request.

For a successful response, its continuation decision is:

| Response | Request held count | Returned log count | Attempt another per-room sync? |
| --- | --- | --- | --- |
| `isOK=true` | 300 | Any | Yes, subject to the coordinator's dispatch guards |
| `isOK=true` | Below 300 | Any | No |
| `isOK=false` | Any | Positive | Yes, subject to the coordinator's dispatch guards |
| `isOK=false` | Any | Zero | No |
| Nil or unsuccessful | Any | Any | Failure completion; no normal continuation |

This is distinct from bulk `eof` handling. A continuation recalculates bounds
from updated room/database state; it is not sparse-log-ID subtraction or a
reverse-page request. No oldest-versus-newest server-window conclusion follows.

## D. Persistence, feeds and read state

Bulk persistence groups returned messages by chat, tracks the returned extrema,
updates the last-message projection and monotonically raises `lastMChatLogId`.
It removes loss marks in the open returned interval and checks linkage to the
next known message. The scheduler then reads the updated bulk cursor for its
next `sinces` array. The bulk callback does not directly set `lastSyncLogId`
or `lastSeenLogId` in its per-room cursor-update block.

The single-room callback behaves differently. In the `isOK=false` branch it
raises local `lastSeenLogId` to the captured request maximum when needed,
raises `lastSyncLogId` and `lastMChatLogId` to the returned maximum when needed,
and repairs the recovered marker interval. The `isOK=true` branch sets
`lastSeenLogId` and `lastSyncLogId` to the captured request maximum, raising
the bulk cursor where needed. Other watermark reconciliation occurs in this
callback as well. This is further evidence that the official sync routine is
not an independent historical-delivery cursor suitable for copying wholesale
into the bridge.

Both history callback families distinguish hidden messages and process
historical feeds. The post-database phase applies rewrite, multi-rewrite,
delete-all, moderation and modify reconciliation. Unresolved modified-log
targets enter the pending exact-message set and can cause `GETMSGS` requests.
This proves a dependency on historical feed application; it does not prove
whether the server returns original content plus feeds or only current content.

Single-room synchronization accumulates thread IDs and reaches the already
traced **local** thread reconciler. The chat controller can also start
chat-log metadata synchronization after sync, controlled by its stop/revision
state. Inventory room updates have a `MEMBER` dependency for missing member
information. These must be counted separately from history-page requests.

No generic "one MEMBER/reaction/media request per history page" count follows
from this inventory. Exact metadata paging, author-profile misses, reaction
revision handling, and view-triggered thumbnail/download dependencies need
their respective consumer dossiers. Existing media and reaction acceptance
does not substitute for historical-page acceptance.

For a regular chat, the local read-divider starts from the greater of the
room's own watermark and saved `lastSeenLogId`. It validates the corresponding
local message and can fall forward to a suitable local message when that item
is absent or unsuitable. This is a display position, not a network read
acknowledgment or an incoming-message notification.

The notification manager's database-fetch observer responds to inserted/deleted
room objects by updating new-message counters and the dock badge (and a
separate favorite-room counter path). That observer does not enqueue a message
notification. Notification delivery instead has a separate message-enqueue
completion, with self-author/referer gating and downstream notification
preferences and notified-log state. Neither traced history callback calls the
enqueue selector; its resolved selector callers are elsewhere in the receive
machinery. This supports separation of historical persistence from immediate
message notification on these paths, not a universal proof that no indirect
observer can notify. Historical media loading likewise has a separate thumbnail
download/cache path; the page's database insert is not itself a download call.

`NOTIREAD` remains
the separately documented open-window/no-watermark-hold/non-self-author path
(SL-BIN-021). Local seen-position writes, `CHATONROOM`, `SYNCMSG` and
`NOTIREAD` are distinct mechanisms. None proves that arbitrary `cnt=0`
requests or bulk startup have no server read effects. Backfill must preserve
its independent delivery journal and read policy.

Stored cursor fields supply a restart lead, but this analysis does not prove
atomic durability across interruption between log writes, feed application,
cursor updates and pending-set retirement. That requires an executed storage
harness or a controlled restart experiment, not a persistence-column inventory.

## Coverage of the request and remaining evidence

| PR #336 area | Static result | Remaining evidence |
| --- | --- | --- |
| A1–A5: eager startup | Bulk trigger, predicates, cursor preservation, order, 100-room cap, callback continuation and ordinary-startup separation recovered. | Confirm fresh-install output/window and list-render timing. |
| B1/B3/B5: chat open, response, amount | Local-first display, CHATONROOM separation, response fields and SYNCMSG continuation recovered; display limit 50. | Execute model decoding; observe initial remote interval/window and viewport behavior. |
| B2: which end is returned | No static server implementation is available. | Bounded owned-account differential; HOLD. |
| B4/C3: boundaries | Global login/SYNCMSG `minLogId` source and `jsi` consumers recovered. | Classify server boundary meanings; execute edge inputs. |
| B6/F1–F3: reads/notifications | Local seen writes, divider, watermark gating, NOTIREAD and database badge updates separated from notification enqueue. | Observe reads independently of fetch; indirect notification consumers remain a completeness gap. |
| C1–C2: older/end-of-history | Immediate scroll path is local backward paging; bulk EOF and single-room continuation differ. One message-restore precheck reaches cloud configuration and is excluded. | Marker UI bindings remain a static gap; remote exhaustion needs observation. |
| C4/H1–H2: state/resume | Separate stored bulk/sync/seen positions and callback writes recovered. | Full atomic interruption/resume proof remains open. |
| C5/G2–G3: errors/retries | Nil/unsuccessful branches recovered; no dedicated bulk delay/throttle-code handling in this chain. | Shared transport timeout/disconnect evidence is SL-BIN-024; no observed history-specific throttling policy. |
| D1–D3/D5: retention, joins, formats | Client predicates cannot answer these server questions; historical feed consumers identified. | Controlled server observations; HOLD. |
| D4/E: media and side requests | Exact-edit recovery, local threads, room/member and chat-log metadata dependencies identified. | Full historical media/author/reaction consumer accounting remains a static gap. |
| G1/G4: pacing/envelope | Serial main bulk chain, numeric order, 100-room request cap, controller-local sync guard. | Global overlap cap and actual largest source-visible burst unproved. |

## Next work and authorization

The initial static pass ran under **HOLD before any live experiment using
the test accounts**. The maintainer lifted that hold on 2026-10-13 and requested
continued research on PR #336. This authorization does not change the
owned-profile, bounded-attempt, cleanup and cloud-restore exclusions.

HST-EXEC-001 subsequently executed the request/response models on synthetic
inputs. It does not execute these static control-flow fixtures. Additional
static work should close the explicit
consumer gaps above before any implementation claims complete parity.
Next, observe a small existing direct/group interval
and unread state, then compare bulk startup and chat open on a fresh official
profile. Avoid speculative bursts or device changes just to answer a window
question. Cloud backup/restore remains excluded.

Transfer review: Codex, 2026-10-13; binary-analysis source class. Redaction and
expression review passed: only protocol behavior, necessary field names,
original behavioral descriptions and synthetic inputs are public. No raw
disassembly, proprietary expressions, account values or private artifact paths
are included.
