# Bounded regular-group history

Evidence date: 2026-10-10. Owned text-history, read-room and unavailable-interval acceptance is recorded below. PR #275 passed all six required CI checks and was squash-merged.

Existing reconnect catch-up retrieves only gaps after a previously committed
room maximum. It intentionally excludes never-committed rooms, and its live
commit queue cannot serve as an independent historical delivery cursor. Operator-driven history uses a separate durable delivery journal.

The Mac 26.8.0 SYNCMSG contract distinguishes missing messages from a requested
page size. Its per-room caller obtains `cur` from the last synced position and
computes `max` and the locally held message count from database state. `cnt` is
that held count, capped at 300; zero declares no locally held messages in the
selected interval. Existing direct-room observations verify that zero can return
missing messages when positive counts return none. They do not establish group
retention, membership visibility or unread effects.

A new static inventory separately identifies an exact-message coordinator and
sender accepting chat-ID and log-ID arrays. The sender constructs a request
model and invokes the shared carriage transport with a completion block. An
unsynchronized modified-log consumer also exists. The Swift coordinator checks
that both arrays are present, compatible with its Int64 conversion and equal
in length before dispatch; invalid input completes without dispatch. The sender
callback passes nil onward for a nil packet and constructs a typed response
for a nonnil packet. This is not a success-status gate. The model fields and coordinator status handling were separately traced below.
Element bounds, higher-level persistence, downstream consumers and visibility
remain unverified on that exact-message path. Exact-message retrieval must not be
assumed to provide general historical pagination.

The implementation policy requires each history action to explicitly
select one source-visible regular group and a bounded interval. Durable history
progress must remain independent of live commit and read watermarks. Delivery
must use existing source message identities to reconcile live overlap, preserve
ordering and attribution, and resume interruption without duplicates. Empty or
unavailable history must be reported without manufacturing content or moving
progress across an unverified gap. The acceptance evidence below identifies which properties were verified.

Local checks and secret scans pass. Required CI and squash merge remain.

Isolated signed offline Mac execution confirmed the exact-message method
`GETMSGS` and request JSON containing `chatIds` and `logIds`; empty arrays are
serialized by the model. It also executed SYNCMSG counts 0, 1 and 300 with the
expected `chatId`, `cur`, `max` and `cnt` fields. Both response models preserve
empty arrays and decode omitted/null `chatLogs` as nil. Production SYNCMSG
request/decoder tests consume the
[executed fixture](../fixtures/group-history/model-runtime.json); mooo's nil
normalization is an explicit deviation. No GETMSGS production code or live
history/visibility claim follows from these model executions.

Static tracing of GETMSGS's coordinator completion establishes a status-zero
gate: nil responses and nonzero statuses complete unsuccessfully; status zero
passes the converted collection, falling back to an empty collection for missing
logs. The typed sender callback alone does not establish this success behavior.
Higher-level persistence and UI/history callers remain gaps; the static
collection converter is described below. The isolated harness had no network entitlement, used a separate bundle
identity, verified original preference hashes and killed its process.

The exact-message collection converter attempts to cast every item and returns
nil on a failed element cast; its caller falls back to an empty collection.
This static behavior does not prove unavailable history or partial-recovery
semantics and must not be treated as successful durable historical delivery.

Owned three-person regular-group differential (Android 26.8.2, original B
secondary profile): A sent one synthetic text while B/C official clients stayed
outside the selected chat and all bridge owners were stopped. A's visible
unread indicator was two before login and while the probe paused after
LOGINLIST/owned-roster verification. One receipted SYNCMSG request for the
selected one-message interval with `cnt=0` returned the exact selected log/text,
and the indicator stayed two. A fresh second text likewise showed two before
login and immediately before a separate receipted `cnt=1` request. That request
returned no logs; after settling, both texts showed one unread participant.
Thus the tested zero-held retrieval did not visibly acknowledge reading, while
the positive control did. Request dispatches and native sends were never
repeated; transient screenshots were deleted after recording the observation.
This establishes one controlled group differential, not universal server behavior,
retention, pre-join visibility, restart semantics or complete history acceptance.

The working implementation adds `history-group <after log ID> <through log ID>
<maximum 1–1000>` in an existing selected room, or `history-group resume`.
It serializes with group delivery, verifies current source membership and a
fresh regular-room ceiling once per command, before its first page, sends
zero-held requests once, and never runs from bootstrap or a ticker. One
`CHATINFO` and one `MEMLIST` serve every page of the command (audit finding 16):
the ceiling only grows, and live membership feeds keep the stored roster
current between pages.

The command is a deliberate, opt-in deviation from the official client. Native
older-history scrolling is local-only and has no remote reverse page
([HISTORY-STATIC](HISTORY-STATIC.md) §C); the bridge pages the source backward
only when an operator runs this command, under the shared history pacing. A separate durable journal reserves
each source request before dispatch and checkpoints each confirmed delivery.
Interruption and uncertain requests require explicit resume. Exhausting the
message budget requires a new explicit bounded selection. Source-empty or
non-progressing responses retain progress and report unavailability.

Historical messages use the existing conversion and source-message identity
path without entering the client's live commit queue or writing read watermarks.
Historical membership feeds become notices rather than replaying membership
changes into the current roster. Full-page interval/order/cursor validation
precedes delivery. A failing regression showed an inconsistent advertised
cursor could previously deliver a prefix before rejection; validation now
rejects it without delivery or history-position advancement. Focused production
tests cover independent live/read state, unavailable and foreign-room logs,
partial delivery failure and explicit resume through a newly constructed client
using the same durable journal. Actual framework delivery tests verify that an
already mapped live message is not sent twice and that later historical logs
retain their own mappings. Historical sends can temporarily join a former
member's ghost through the Matrix SDK; roster reconciliation to the bridge's
current verified roster (no new source read) runs after both successful and
failed sends, before advancing historical progress.
Regression tests reproduce and protect both cleanup paths.

Historical Matrix sends keep the SDK's encryption, joining, profile and double
puppet send path. An HTTP adapter supplies a stable transaction ID scoped to the
Matrix room, sender, bridge, login, source message, part and event type. A
production transport test simulates server acceptance followed by a lost
response, then retries through a new transport: the server applies one event.
Auxiliary key requests retain their original paths. This verifies transaction
identity and transport behavior with a synthetic server; encrypted homeserver
acceptance, multipart history and double puppet restart remain unverified.
The SDK's concrete Matrix connector and intent types remain unchanged: built-in
commands and migration helpers assert those types. A regression reproduces the
earlier wrapper's incompatibility and protects the concrete types. Historical
conversion marks a part before serialization; a crypto-interface decorator
removes that private marker before delegating encryption, and the HTTP adapter
uses its stable transaction. Plaintext sends strip the marker before dispatch.
Synthetic regressions verify both boundaries and preserve unrelated fields.

Historical conversion requires exactly one part. The SDK treats any mapped part
as a duplicate source message; allowing multiple parts could silently omit an
unsent part after partial success and resume. Such conversions are rejected
before any Matrix message is sent, leaving progress available for explicit
resume. External-homeserver double-puppet intents with a separate HTTP transport
are also rejected before sending. Historical multipart and external double
puppet support require additional recovery work; neither is claimed here.
The initial encrypted owned-account results below exercised the earlier
adapter. A second fresh owned A/B/C group separately exercised the revised
crypto/HTTP boundary: two pre-discovery A texts remained unmapped after normal
portal discovery, then one explicit encrypted bounded command imported both.
The retained Matrix SDK decrypted both to their exact expected text, with the
same A ghost, source order, two mappings and a completed durable journal.
Normal restart and an explicit bounded replay preserved both mappings exactly
and retained exactly two encrypted source-author events. Both bridge processes
completed graceful shutdown without a panic.
The official B and C clients also showed both exact texts once in the same
three-member native group.
The scoped owned-account acceptance is recorded below. General retention,
pre-join visibility and the unverified send variants remain explicit gaps.

Normal owned-account catch-up with the history adapter installed connected using
the original secondary profile and delivered the two existing differential
texts into the encrypted selected room. All 17 previous mappings remained
unchanged; two new mappings retained source order and author identity. The
retained Matrix SDK decrypted both to their exact expected text. This validates
the ordinary reconnect path with the adapter installed, not explicit older
history retrieval, stable historical transaction use or history restart parity.

A separately receipted zero-held request selected only the interval before the
first mapping in that same owned group. The production history reader reported
that interval unavailable and produced no events. No mappings or live/read
checkpoints were written by the probe. This establishes an unavailable interval
in the selected account and room; it does not establish a general retention
cutoff or prove that visible older text can be imported.

Fresh owned A/B/C regular-group acceptance: A created a new native group while
the bridge was stopped and sent two synthetic texts once. Original B's full
inventory identified the latest exact text and confirmed the three owned
member IDs. Zero-held source retrieval first returned a membership event, then
a separately receipted next-cursor request returned both exact texts in order
and attributed to A. Neither text had a Matrix mapping. Normal bridge discovery
created one portal and left both texts unmapped. The selected acceptance room
was explicitly configured for Megolm and its owning test login was granted
room-administrator power by the configured bridge creator. History commands
require room permission for the bridge state event as well as the owning login.

One encrypted bounded history command imported the first text. A monitor sent
SIGINT to the recorded bridge process as its first mapping persisted; shutdown
completed successfully with exactly one mapping and the durable journal at the
first log, nine remaining messages and `done=false`. The Matrix SDK decrypted
the historical event to the exact expected text. Normal restart connected and
left that single mapping unchanged. Explicit encrypted resume then imported
the second text, preserved the first mapping and completed the journal at the
selected upper bound. The SDK decrypted the second historical event exactly.

A separately receipted third text sent by A while the bridge was live produced
one further encrypted mapping. Explicit history retrieval over all three logs
preserved all three mappings without adding another source-author event. The
encrypted timeline retained source order and the expected A ghost; all three
events decrypted exactly. The earlier acceptance room retained all 19 prior
mappings unchanged. This establishes text history, graceful interruption,
explicit restart/resume and overlap with live text for one owned regular group.
It does not establish arbitrary retention, pre-join visibility, abrupt-process
crash recovery, historical multipart encryption or double-puppet parity.

After both B and C opened the fresh native chat, each official client showed
all three exact texts once and in source order. A subsequent CHATINFO omitted
the last server log, last seen log and last chat log while retaining the correct
room and three-member roster. A separately obtained full LOGINLIST still carried
the same room's positive last-server/last-chat-log values and a sync target
beyond the second historical text. Thus absent CHATINFO log fields do not prove
that this account lacks history access. The original guard rejected a valid
read-room interval before making a history request.

A failing production regression reproduced that rejection. The guard now uses
the original account's login-inventory/observed-live ceiling when CHATINFO omits
its ceiling. Fresh authoritative membership and regular-room verification remain
required; Matrix mappings never supply authority. Regression cases also reject
a different room's inventory maximum, a maximum below the selected upper bound
and an absent target, all before a history request. Owned-account encrypted revalidation succeeded as described below.

With the fix installed, one explicit request for the previously observed
unavailable interval reserved its journal before dispatch and stopped with
`request_pending=true`, one attempt and its starting cursor unchanged. All 19
existing mappings remained unchanged. The Matrix SDK decrypted the exact
bounded unavailable notice, including the explicit resume/reconnect guidance.
A subsequent valid replay in the already-read fresh group completed; its
encrypted completion notice decrypted exactly, and all three mappings and
source-author events remained unchanged. This verifies the inventory fallback
on the owned source path, rather than only against the synthetic regression.

An unfinished selection cannot be overwritten by another selection; resume is
explicit. There is currently no command to discard an unfinished selection.
An unavailable interval therefore stays reserved until it can complete or its
budget is exhausted. No automatic request retry or widening of bounds occurs.


### Returned-floor classification (2026-10-13)

The production response parser now preserves the client-wide `minLogId` supplied
by SYNCMSG. When an empty response supplies a floor strictly above the explicitly
selected ceiling, `ReadHistoryPage` returns `ErrHistoryBelowFloor` together with
`ErrGapUnresolved`. It returns no events and advances neither history completion
nor live/read checkpoints. Empty responses with no floor, or a floor at/below
the ceiling, remain unresolved rather than being labeled below-floor or complete.

The production client regression consumes both sanitized HST-LIVE-001 cases from
`research/fixtures/history-startup/observed-window.json`. This classifies the
observed unavailable selection; it does not establish a fixed retention duration
or a successful import. Automatic pacing/cache recovery remains separate work.


### Acquisition admission (2026-10-13)

Clients opened from operator profiles now route zero-held-count SYNCMSG through
one serialized admission object. Identical concurrent room/cursor/ceiling requests
share one source attempt and retain their separate delivery limits. The bridge
exposes `network.history_pacing.minimum_interval` (default 2 seconds),
`error_backoff` (30 seconds), `max_pending` (32 demands, including coalesced
waiters), and `maximum_duration` (1 minute for admission plus one source attempt,
also when the caller has no deadline). These are conservative operator defaults, not measured server limits.

The private profile's `.history-admission` file binds the cooldown to its account.
Before dispatch the client atomically reserves the error cooldown; after a validated
progressing response it records normal spacing, measured from response completion.
Transport/decode failures and empty/inclusive-only pages keep the longer cooldown.
A corrupt, wrong-account, unsafe-permission or failed-write state prevents dispatch.
No request is automatically repeated. Catch-up still records its unresolved durable
gap when admission detects non-progress, and changes no delivery/read watermark.
Shutdown cancels waiting acquisition and joins persistence before releasing the
profile lease. Explicit positive-held-count read acknowledgements are separate.

This slice does not yet persist acquired messages or prevent later sequential
refetches. Dependency metadata/media requests and MCHATLOGS remain outside this
admission path. Separate copies of the same account profile do not yet share a
worker; account-wide coordination across those copies is an outstanding gate.
The three-flow goal remains incomplete. Tests execute the production history reader,
carriage request, admission and checkpoint paths against synthetic responses;
this establishes worker mechanics, not an official-client rate or parity claim.


### Acquired-page store (2026-10-13)

The production profile now owns a private `.history-store` directory (0700,
records 0600), bound to account and device identity. Validated source pages and
current target records are written atomically before acquisition returns. They
are independent of live commit and read watermarks. Returning one event does not
discard the rest of the source page. Cache reads combine connected acquired
prefixes, preserve sparse identities/order, and never broaden a selected ceiling.
A raised ceiling can reuse its known prefix without fetching it again.

Live edit/delete observations and historical feeds update durable target records
before page publication. Known deletion is terminal for that target and prevents
retained text from being rendered. Edits with a replacement update older snapshots;
known edits without the target block that acquired position until reconciliation.
Unrelated acquired history stays available. GETMSGS results can supply current
replacement records. Buffered catch-up events are projected through target state
before delivery admission, so a later delete feed cannot leave its old text in an
earlier buffered message. Unknown/unavailable edit targets can still pause catch-up;
automatic bounded target reconciliation is remaining integration work.

Cached reads require no source request and bypass source admission cooldowns.
Corrupt, mismatched or unsafe storage fails closed, rather than silently becoming
an acquisition miss. Each serialized record is bounded by the private JSON limit;
capacity/write failures are explicit and advance no delivery checkpoint. Mutation
records precede page writes, so already durable target updates survive an interrupted
later page write. Unpublished temporary files do not count as acquired coverage.

[HST-CACHE-LIVE-001](../fixtures/history-store/observed-cache-window.json) used one
owned three-member regular group. The first source page held one record; the next
held four. The continuation returned one record, then the other three from storage.
A reopened profile reproduced identities, content and order with both HTTP and LOCO
transports denied (zero attempts). Local delivery/read cursors remained unchanged.
The first probe stopped on an incorrect assumption that its first page must contain
multiple records; cleanup ran, and the continuation reused its private saved bounds.
There were two source page requests in total, across the two bounded observations.

This proves SDK store behavior, not official UI parity, encrypted Matrix insertion,
or server unread neutrality. Two records remain explicit message gaps. Startup
MCHATLOGS, room viewing, backward bridge pagination, metadata/media pacing and
complete account-wide integration still require the remaining three-flow gates.
