# Acquired-history bridge delivery and pagination

Implementation/evidence checkpoint: 2026-10-14 UTC (2026-10-13 PDT). This advances
the three-flow goal; official startup UI, viewing dispatch/read effects, account
ownership and dependency pacing still have open gates.

## Consuming paths

With `network.startup_history` enabled, chat snapshots advertise `CanBackfill`.
The standard framework can perform initial forward delivery when it creates a
portal, and regular-group resyncs request forward history. Both still require the
framework's `backfill.enabled` configuration and its configured message limits.
The connector now implements `BackfillingNetworkAPI.FetchMessages`, so existing
provisioning pagination calls can reach actual Kakao history code. Default remains
false. The separate explicit `history-group` command remains available.

The production SDK's `ReadStoredHistory` selects the newest acquired rows in an
explicit source-ID interval and returns them in ascending identity order. It
reports both older acquired rows and coverage of the selected interval. A backward
anchor is exclusive; an empty portal includes the inventory's latest identity.
There is no subtraction of a page size from a sparse log ID. Stored current edits
and deletion flags apply before decoding. An unresolved edit target fails rather
than rendering stale content. Cache reads never connect, fetch or acknowledge.

Forward fetches require startup acquisition coverage. Backward fetches use stored
rows first. If a missing interval needs acquisition and `AllowSlowFetch` is false,
return an empty slow-fetch deferral; the pinned framework otherwise discards a
batch carrying that flag. Explicit/allowed slow fetches acquire forward from zero
to the selected anchor through the shared SYNCMSG worker, at most 25 page-reader
iterations and 30 seconds. This is bounded missing-range reconciliation, not a
new remote reverse-page command. Partial source progress remains in the store.

Backward timeline insertion requires advertised batch-sending capability. Standard
Synapse's ordinary send API cannot insert older events at their historical timeline
position. Forward legacy sends have a stable per-part transaction adapter. Batch
sends use the framework's deterministic event identities; missing mapped parts
are selected independently, including multipart messages.

## Delivery and restart invariants

A separate private bridge journal records each selected window before conversion
or sending: direction, lower/upper identity bounds, first selected identity, limit,
anchor exclusion and pending state. A retry uses that window even when framework
anchors or preferred count change. Already mapped parts are omitted. Completion
requires confirming every expected visible message part, then publishing the
journal. Historical edit/delete feeds have already affected target projections;
they are not replayed as current Matrix edits/removals. Historical membership
feeds render notices rather than applying old membership as today's roster.

The pinned executor assigns backward cursor/completion flags before sending and
credits a batch afterward. If mapping or journal confirmation fails, the completion
callback restores the old cursor, keeps the task eligible, and compensates that
pending batch credit. A missing mapping must not become a completed task. This
adapter is tied to the audited dependency and has a production/framework regression.

Framework forward backfill logs send/conversion errors instead of returning them
through room resync. Bootstrap checks its durable forward journals before reporting
ready. An unresolved selection pauses startup; explicit reconnect resumes it. Live
committed positions remain separate from acquisition and history delivery.

## Source ordering versus framework timestamps

The pinned generic cutoff discards rows based on anchor timestamps, including
distinct identities sent in the same second. Kakao's traced local/history ordering
uses source log IDs. The connector selects and deduplicates by scoped source
identity first, then adapts the framework's freshly loaded comparison object to
retain that selection. It changes the temporary timestamp boundary and, for a
missing part of the anchor itself, clears its temporary comparison ID. Neither the
database anchor nor outgoing message identity/timestamp changes. Regressions
exercise the actual framework cutoff and reread the database afterward.

The framework also omits remote-event context mutation between historical
conversion and sending. Legacy conversion therefore marks each stable part
transaction. The crypto adapter strips that marker before encryption and carries
it outside ciphertext to the HTTP adapter, which selects the stable transaction
and removes the marker before transmission. Unmarked traffic retains SDK behavior.
Plaintext/encrypted marker paths and existing transaction tests cover this boundary.

## Controlled evidence

Synthetic tests execute real SDK stored-window logic and real pinned framework
cutoff, legacy/batch send, message database writes and completion paths. They cover
sparse IDs, exclusive anchors, current target projection, missing coverage, invalid
scope/cursor, same-second ordering, partial delivery, a lost batch response,
restart selection, and unconfirmed mapping/task completion. These are integration
regressions, not executed official-client coordinator/UI parity fixtures.

HST-BRIDGE-CACHE-009 reused the previously observed private five-record history of
one verified three-owned-member regular group. An owned SDK session and actual
framework fetched/converted/delivered it to a **synthetic Matrix batch sink**. The
first sink response was deliberately lost after applying the batch. A fresh
connector and task recovered the private selection, yielding two batch attempts
and five distinct event identities. No SYNCMSG or MCHATLOGS request occurred; local
source read/delivery cursors stayed unchanged. The source session and original
profile lease were released. No real Beeper deployment was contacted.

The [sanitized fixture](../fixtures/history-startup/observed-bridge-cache.json)
contains only aggregate outcomes. This does not establish owned encrypted Matrix
history insertion, native UI ordering/read badges, or full message-body fidelity.
The two existing vote-history gaps remain notices; five projected events is not a
claim that all five original bodies are supported.

## Remaining gates and limitations

- Complete official fresh startup through visible history and peer read effects.
- Provide and exercise a viewing dispatcher; standard appservice traffic still
  does not call `HandleMatrixViewingChat`. Trace and pace its CHATONROOM and
  missing-range/dependency work before claiming room-open parity.
- Exercise the actual authenticated pagination HTTP route with this connector,
  including simultaneous/repeated demand, source pacing and encrypted insertion.
- Validate former-author roster effects, history replies/reactions/media, and
  current target resolution. Existing live media acceptance does not prove those
  historical consumers. Membership notices alone do not prove ghost-send roster
  preservation on every backend.
- Scope the worker across profiles for one account and include dependent metadata,
  exact-message and media calls. Current worker remains profile-scoped and those
  dependencies are not all paced.
- Review Matrix `unread_hours_threshold` separately: the framework may mark old
  batches read regardless of the connector response. No source read acknowledgement
  is issued by these fetch methods; local cursor invariance does not prove peer
  unread state or notifications.

## Authenticated provisioning route checkpoint (2026-10-14)

`TestAuthenticatedHistoryHookActualCacheAndLostBatchRestart` now drives the
pinned framework's real authenticated
`POST /_matrix/provision/v3/backfill/{roomID}` handler, actual KakaoClient
FetchMessages, real SDK ReadStoredHistory over a synthetic private acquired-page
fixture, SQLite selection/mapping/task state, real Matrix connector batch
serialization, and a fault-injecting synthetic HTTP boundary. The
inventory adapter is synthetic; no LOCO acquisition is allowed by the harness.

The first batch is accepted by the sink but its response is lost. No Matrix
mapping is confirmed, and the pending source window persists. A fresh SDK,
connector, Matrix connector and SQLite reopen recover that window and retry the
same deterministic event IDs and complete event content in the same chronological
order. Bodies, author MXIDs and original timestamps are checked. Confirmed
mappings match the IDs actually sent over HTTP; the recovered page and a second older page leave exactly four
unique historical events across three HTTP batch attempts. Both pages retain
ascending order, while the cursor selects the older window on the next request.
A final authenticated request returns HTTP 204 for the exhausted task without
another local read or source acquisition.

Unauthenticated requests return 401, a logged-out login returns 403, and missing
batch capability returns 404, all before reading history. Both successful and
failed attempts leave source live-delivery cursors/read watermarks unchanged;
neither explicit source ACKs nor Matrix read markers/notifications are issued.
These effects describe this backward-delivery case, not opening/reading the room.

Run with `GOFLAGS='-tags=goolm' go test -race ./internal/bridge/connector -run
AuthenticatedHistoryHook -count=1`. This is a production-path synthetic contract
check, not official-client parity, an owned encrypted Matrix deployment, or a
Beeper UI observation. The fixture represents already acquired history; it does
not prove clean login or live gap acquisition. Existing worker tests cover source
pacing independently. Owned encrypted acceptance and a transport that supplies
room-opening/viewing demand remain required before completing the three flows.

## Exact unavailable-window reuse (2026-10-14)

The observed HST-LIVE-001 empty old-window cases now run through the production
acquired store and admission. Their original regression fails before the fix:
a repeated read tries the already-closed source session instead of reusing the
known below-floor result. Both cases still produce ErrHistoryBelowFloor and
ErrGapUnresolved, with no message, delivery cursor or read watermark advance.

A successful empty zero-held-count SYNCMSG response whose minLogId exceeds the
selected ceiling now persists an account/device-bound unavailable observation
for that exact room/cur/max. It is separate from positive acquired pages and
bulk progress. `HistoryUnavailableError` carries the floor, observation time and
retry time; `ReadStoredHistory` exposes the same observation with Complete=false.
The bridge returns the classified error, performs no new acquisition and keeps
the framework task unfinished. It does not turn unavailable history into EOF,
a delivered selection, or successful startup coverage.

Freshness is **five minutes from observation**, a chosen local cache policy,
not a Kakao retention duration or an official pacing rule. Reads and restart do
not renew it. Expiry allows the next demand through normal account admission;
it does not start an automatic retry. Another room, cur, ceiling or positive-held-
count ACK cannot reuse the observation. Positive acquired coverage takes
precedence. Missing/unexplained empty responses are not negative-cached, and no
permanent or global floor is extrapolated from one response.

Private files contain only the scoped request, binding, floor and times. There
are at most 1,024 fresh observations per profile; expired records are pruned
on opening/saving. Invalid binding, schema, request, timestamp, filename or file
permissions fails closed. Corrupt indexed files never silently become source
misses. The cache does not claim that the server will never make an interval
available later; bounded expiry permits revalidation.

Race-enabled checks cover both observed projections, exact scope, real opened
profile restart with dialing denied, unchanged freshness, expiry entering actual
admission/session opening, positive-cache precedence, unexplained empties,
corruption/timestamp/capacity guards, and an actual framework task remaining
unfinished without acquisition. Cache policy is an implementation decision;
these checks do not establish official-client timing parity. No live account
request was used for this slice. Owned encrypted delivery and the complete
clean-login/open/scroll acceptance gates remain open.

## Owned encrypted delivery and response-loss recovery (2026-10-14)

**HST-BRIDGE-ENC-010** runs mooo `4367e580`, the pinned framework with goolm,
and owned disposable Synapse 1.162.0 on macOS 26.6.2 arm64/Go 1.27.2.
The [sanitized observation](../fixtures/history-startup/observed-encrypted-delivery.json)
records counts and invariants; all account-specific artifacts remain private.

The existing owned secondary profile and its acquired five-record regular-group
cache were exclusively leased under a common scheduling root. Source TLS,
secure-carriage, HTTP and external resource transports were denied. A scoped
inventory snapshot adapter selected the cached room; no fresh inventory/roster
request or source login ran. A SQLite backup preserved the existing bot's Olm
identity, checked against server keys before crypto startup. One fresh owned
encrypted Matrix room supplied an empty delivery target without touching the
original bridge database or its rooms.

The real FetchMessages/forward framework path converted that cache, used real
crypto and sent over actual Matrix HTTP. The first send reached Synapse, but its
response was deliberately withheld. Its mapping remained unconfirmed and the
pending selection persisted. A new SDK, connector, Matrix connector and database
process reopened the same private state and recovered the window. The previously
accepted event kept its event ID; the complete timeline contained exactly five
unique encrypted events in source identity order.

The owned tester SDK decrypted all five events. Bodies/types matched conversion,
senders matched confirmed mappings, and all five known source timestamps were
preserved. One TEXT body also matched the cached raw source document directly.
Conversion was identical across restart. Source read/live cursors stayed empty;
source network dispatch was denied throughout. The initial receipt records zero
source attempts. A third fresh reopen, with Matrix and
source requests rejected, returned no new messages or network attempts.

The first decryption assertion failed because the private harness omitted the
SDK's explicit encrypted-content parsing step after fetching an event. Parsing
and auditing the already retained ciphertext resolved that harness error; no
message was resent to diagnose it. The offline replay guard likewise retained
the production transaction adapter while rejecting its underlying transport.
All probe processes and the temporary appservice listener stopped afterward.

This closes the owned **cached forward delivery/encryption/restart** gate for
this window. It does not establish fresh official startup, native room-open/read
behavior, current roster state or Beeper gesture routing. The two existing
type-14 gaps still render notices and remain original-content fidelity gaps.
Subsequent offline cache inspection identifies both as unverified subtype-4
poll lifecycle cards, without option lists; see [the observed shape and next
evidence required](../vote.md#historical-lifecycle-cards-offline-cache-inspection).
Standard Synapse lacks backward batch insertion, so this run does not claim
faithful insertion before existing events. The complete three-flow goal remains
open; acquisition, unavailable boundaries, media/replies/edits/deletions and a
batch-capable historical deployment require their own declared acceptance cases.
