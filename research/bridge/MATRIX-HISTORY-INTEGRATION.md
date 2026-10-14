# Matrix/Beeper history integration investigation

Implementation update (2026-10-14 UTC): the connector now implements opt-in
FetchMessages and acquired-history initial delivery. The findings below retain
the dated audit baseline; see [current paths and evidence](HISTORY-BRIDGE-PAGES.md).
The standard appservice still supplies no room-viewing signal.

Date: 2026-10-13. Scope: clean bridge login, opening an existing portal and
backward scrolling on the Matrix side. Kakao requests, accounts and experiments
were held throughout. This is a findings record, not a replacement of the history
plan or authorization to enable automatic fetching.

## Method and confidence

Source audit of mooo on main after PR #346 and the exact dependency
`maunium.net/go/mautrix v0.31.1-0.20260929131313-40942319c433`, plus the Matrix
specification and Beeper bridge-manager source at
`d04569b64f21d495e75eae08b784ded325a33839`. One unauthenticated read of the owned
local Synapse `/_matrix/client/versions` endpoint checked advertised capabilities.
No live bridge process, source-network client, real account login, appservice
registration or user-message write was started. A subsequent local HTTP harness
initializes framework objects with a synthetic login and disposable SQLite state. No Beeper UI observation was performed.

Source-derived framework behavior is high-confidence for this pinned version.
Actual current Beeper Desktop/mobile behavior and custom-bridge routing remain
unobserved. Interface availability is not proof that a deployed client calls it.

## Answers

| Question | Finding | Evidence boundary |
|---|---|---|
| Does opening a standard Matrix room notify the bridge? | No standard room-open signal; the appservice connector does not call the viewing interface. | Explicit framework contract and no invocation in the pinned module. |
| Does ordinary backward scrolling call the bridge? | `/rooms/{roomId}/messages` retrieves homeserver history. It is not the framework provisioning backfill endpoint. | Matrix client-server contract; custom client/server integrations may add another call. |
| Can Beeper request source backfill? | The framework exposes authenticated `POST /v3/backfill/{roomID}` under `/_matrix/provision`. | Requires batch capability, manual backfill enabled, a logged-in source and an existing task. Actual UI invocation is unobserved. |
| Can the ordinary appservice connector insert older history on Beeper? | It has a Beeper batch-send path, selected by advertised capability. | Source-derived support; encrypted insertion and resulting UI order need owned deployment acceptance. |
| Does standard Synapse support that insertion path? | The owned local server does not advertise `com.beeper.batch_sending`; the framework selects its legacy path. | Observed capability response, not an attempted insertion or UI test. |
| Are mooo's automatic history interfaces wired? | No `FetchMessages` or `HandleMatrixViewingChat` implementation exists in the audited shipped connector. | Existing explicit group importer is a separate path. |
| Does the queue's delay protect manual scrolling? | Not by itself: manual lookup ignores the scheduled timestamp; manual-only execution launches goroutines. | Pinned queue and database query audit. |

## Room-open signals

`ChatViewingNetworkAPI.HandleMatrixViewingChat` exists, but its comment says the
standard appservice connector never calls it. The contract expects a client signal
approximately every five seconds and has no explicit close signal. The pinned
module contains the interface and request type, but no dispatch implementation
for this hook. mooo's executable uses `matrix/mxmain.BridgeMain`, which installs
the standard appservice connector. Setting `homeserver.software: hungry` does
not install a different connector or supply a viewing dispatcher.

Therefore the earlier suggestion that a Beeper deployment might automatically
provide this hook was too broad. We need an actual client/transport integration
before mapping room opening to source chat-open behavior. Read receipts and typing
are separate events; using either as a proxy for opening changes the semantics
and misses opens that produce neither event.

Even with a viewing signal, repeated five-second updates must be treated as a
viewing lease, not repeated acquisition requests. A future integration must also
handle stale leases and multiple devices without multiplying work.

## Scrolling and manual pagination

Standard Matrix clients read recent events through sync and older events through
`GET /_matrix/client/v3/rooms/{roomId}/messages` with a pagination token. Those
requests address the homeserver. The appservice provisioning API is separate.
Do not assume every Matrix pagination request reaches mooo or indicates missing
source history.

The pinned provisioning route calls `PostPaginate`. It rejects requests without
`BatchSending` or `backfill.queue.manual`, selects the source login, finds the
portal and a backfill task, then schedules one manual task. No task gives HTTP
204; completion gives HTTP 200; waiting longer than 25 seconds gives a gateway
error. Cancellation or timeout of the HTTP wait does not prove cancellation of
background acquisition: execution uses the bridge/queue background context.
A client retry can therefore add work while a prior request is still running.

Backward tasks call `BackfillingNetworkAPI.FetchMessages` with an oldest-message
anchor, stored cursor, preferred count and `AllowSlowFetch`. The response provides
chronologically sorted messages, a continuation cursor and `HasMore`; the
framework persists task state after processing. `MoreRequiresSlowFetch` can stop
background work while permitting explicit demand, but is not an account rate
limiter. Initial and catch-up backfill are separate forward paths.

mooo does not currently implement this interface. Merely changing configuration
will not turn the explicit `history-group` command into on-demand client scrolling.

## Historical insertion and encryption

The appservice connector discovers `BatchSending` through the homeserver's
`com.beeper.batch_sending` advertised unstable feature. Its insertion endpoint is:

`POST /_matrix/client/unstable/com.beeper.backfill/rooms/{roomID}/batch_send`

The framework batch path builds historical events and deterministic message-part
IDs, can encrypt events before dispatch, and controls forward insertion,
notifications and read marking separately. Backward batches do not request the
forward-message notification flag. This is source evidence, not a demonstrated
absence of all UI notifications or badge changes.

Standard Matrix sending can preserve original timestamps but does not thereby
insert events at an earlier timeline position. The framework falls back to legacy
individual sends when batch capability is absent. The owned local Synapse
capability query lacked the batch feature. Thus local encrypted messaging
acceptance does not establish Beeper historical-insertion acceptance.

Beeper's bridge-manager supports connecting third-party bridgev2 appservices to
its homeserver. This does not establish that a custom bridge gets automatic
room-viewing or scroll-triggered provisioning requests. Its generated config is
also not a safe policy to adopt blindly: the audited template enables backfill
and the queue, supplies a 20-second batch delay, and does not set `queue.manual`.
Its limits and room-type overrides are generic, not mooo policy.

`unread_hours_threshold` can force old backfill to be marked read on the Matrix
side. That setting must be reviewed separately from source read effects; neither
Matrix marking nor fetching establishes what a source server considers read.

## Request amplification in the pinned framework

1. A portal lock prevents simultaneous backfills in one room, but a waiting manual
   request can execute the next batch after the lock is released. This serializes
   requests without coalescing equivalent demand.
2. With the background queue disabled, `WakeupBackfillQueue` starts a goroutine
   for each manual task. Different rooms can execute concurrently; same-room
   tasks wait on the portal lock.
3. The background task query enforces `next_dispatch_min_ts`. The manual
   `GetNextForPortal` query does not; repeated demand can bypass the delay.
4. Queue wakeups can bypass the loop's delay. The timestamp protects ordinary
   scheduled task selection, not all entry points.
5. Maximum batches limit total task work; they do not enforce account-wide pacing.
   Preferred message count is also not a source request-count limit: one fetch
   callback may internally perform several network operations.
6. An HTTP timeout, no messages with `HasMore=true`, ancillary thread/media fetches
   and retries can add work that a simplistic page counter would miss.

These are integration constraints, not evidence that the current mooo importer
has generated a request storm. Automatic framework backfill is not wired today.

## Executed local HTTP hook (2026-10-13 follow-up)

The maintainer requested calling the hook directly without real Beeper.
`tools/history-hooks/hooks_test.go` now sends in-memory HTTP requests through the
real appservice router, provisioning authentication, pagination handler, manual
backfill executor and SQLite task queries. The source is a counting fake; it has
no Kakao implementation or credentials. It returns empty synthetic batches to
avoid any Matrix message-send path. No live server is needed.

Run with `GOFLAGS='-tags=goolm' go test -race -v ./tools/history-hooks`.

Executed results:

- Missing authentication gives 401, no source callback.
- Missing batch capability or disabled manual pagination gives 404, no callback.
- An eligible login/portal with no task gives 204, no callback.
- A task scheduled an hour into the future is excluded by background selection,
  but the authenticated pagination POST immediately calls the source and gives 200.
- A second immediate POST calls it again despite a one-hour batch delay.
- A third response with `HasMore=false` ends the task; the next POST gives 204
  without a fourth callback.
- Callback parameters preserve the selected portal, backward direction, configured
  count, slow-fetch permission and stored continuation cursor.

This executes the actual framework entry point, not a reimplementation of its
handler. It verifies the pacing constraint directly. It does not execute actual
Beeper UI routing, historical insertion, encryption or source-network behavior.
Calling a synthetic viewing implementation directly would only prove that our fake
method can run: the production viewing dispatcher remains absent.

Useful docs: the [Go API documentation](https://pkg.go.dev/maunium.net/go/mautrix/bridgev2#ChatViewingNetworkAPI)
covers viewing and fetching interfaces; the pinned provisioning source documents
the concrete HTTP route. No standalone public OpenAPI specification for this
pagination route was found in the documentation searched. For this dependency,
the verified full route is `POST /_matrix/provision/v3/backfill/{roomID}` with
provisioning authentication and a selected/default source login.

## Implications to discuss before implementation

A future source scheduler should own every acquisition entry point and enforce
account-wide concurrency, pacing and finite per-demand budgets independently of
framework queue settings. It should coalesce room/cursor demand, serve stored
history first, persist progress and classify unavailable/non-progress outcomes.
Repeated viewing signals or timed-out pagination retries must not each authorize
another batch. Count ancillary source calls as well as primary history requests.

These are proposed requirements, not implemented protections or measured Kakao
limits. No arbitrary numerical source rate is established by this investigation.

## Remaining Matrix/Beeper acceptance questions

- Does an owned Beeper custom-bridge deployment advertise batch insertion, and
  can it insert synthetic encrypted messages before existing messages with
  correct order, timestamps, replies, attribution and restart deduplication?
- Which current Desktop/mobile actions actually call the custom bridge's manual
  pagination route? Does the client retry after timeout, prefetch, or issue
  parallel requests from multiple devices? Capture endpoint/count/timing only.
- Is there an accessible viewing transport for this deployment? If not, what
  explicit integration or supported fallback is needed for room-open semantics?
- What UI distinguishes temporarily pending history, source-unavailable history
  and the true end of acquired history? Does scrolling remain usable after an
  empty Matrix page while bridge work is pending?

These can be tested with a synthetic source connector that has no Kakao client or
credentials. First establish the transport/insertion behavior, then exercise
rapid scroll, repeated viewing, two clients and timeout/retry with a counting fake
source. Keep Kakao held until the maintainer resumes that part of the research.

## Sources

- [Matrix history pagination](https://spec.matrix.org/latest/client-server-api/#get_matrixclientv3roomsroomidmessages).
- [mautrix history limitations](https://docs.mau.fi/bridges/general/backfill.html).
- [Pinned viewing and history interfaces](https://github.com/mautrix/go/blob/40942319c433/bridgev2/networkinterface.go).
- [Pinned provisioning route](https://github.com/mautrix/go/blob/40942319c433/bridgev2/matrix/provisioning.go).
- [Pinned backfill queue](https://github.com/mautrix/go/blob/40942319c433/bridgev2/backfillqueue.go).
- [Pinned task selection](https://github.com/mautrix/go/blob/40942319c433/bridgev2/database/backfillqueue.go).
- [Pinned backfill delivery](https://github.com/mautrix/go/blob/40942319c433/bridgev2/portalbackfill.go).
- [Pinned appservice capability/encryption handling](https://github.com/mautrix/go/blob/40942319c433/bridgev2/matrix/connector.go).
- [Pinned Beeper batch API client](https://github.com/mautrix/go/blob/40942319c433/client.go).
- [Beeper custom bridge deployment](https://github.com/beeper/bridge-manager/blob/d04569b64f21d495e75eae08b784ded325a33839/README.md).
- [Beeper config template](https://github.com/beeper/bridge-manager/blob/d04569b64f21d495e75eae08b784ded325a33839/bridgeconfig/bridgev2.tpl.yaml).
